// Package record captures proxied traffic and turns it into mappings (FR-6.1). A recording
// belongs to a scope, the global session or one shadow mock, and sees the requests that no
// mapping answered while it runs. A snapshot builds the same mappings from the journal.
package record

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
)

// Recording states reported by the status endpoint.
const (
	StatusNever     = "NeverStarted"
	StatusRecording = "Recording"
	StatusStopped   = "Stopped"
)

// Spec is the body of a start-recording or snapshot request, in WireMock's field names.
// RepeatsAsCallResponses replaces WireMock's repeatsAsScenarios.
type Spec struct {
	TargetBaseURL          string                   `json:"targetBaseUrl"`
	Filters                *model.RequestPattern    `json:"filters"`
	CaptureHeaders         map[string]CaptureHeader `json:"captureHeaders"`
	RequestBodyPattern     *BodyPattern             `json:"requestBodyPattern"`
	Persist                *bool                    `json:"persist"`
	RepeatsAsCallResponses bool                     `json:"repeatsAsCallResponses"`
}

// CaptureHeader says how a captured request header is matched.
type CaptureHeader struct {
	CaseInsensitive bool `json:"caseInsensitive"`
}

// BodyPattern says how a request body is turned into a body pattern.
type BodyPattern struct {
	// Matcher is auto, equalTo, equalToJson or equalToXml.
	Matcher             string `json:"matcher"`
	IgnoreArrayOrder    bool   `json:"ignoreArrayOrder"`
	IgnoreExtraElements bool   `json:"ignoreExtraElements"`
	CaseInsensitive     bool   `json:"caseInsensitive"`
}

// PersistOrDefault reports whether recorded mappings are stored; WireMock's default is yes.
func (s *Spec) PersistOrDefault() bool { return s.Persist == nil || *s.Persist }

// Exchange is one request and the response it got from upstream.
type Exchange struct {
	Request *match.Request
	Status  int
	Header  http.Header
	Body    []byte
}

// Active is a running recording.
type Active struct {
	Spec   Spec
	filter *match.RequestMatcher
	// onNew, when set, is called with the mapping for each request the recording has not seen
	// before, as the exchange arrives (WireMock's --record-mappings).
	onNew func(model.StubMapping) error

	mu        sync.Mutex
	exchanges []Exchange
	seen      map[string]bool
}

// Accepts reports whether a request passes the recording's filter.
func (a *Active) Accepts(req *match.Request) bool {
	return a.filter == nil || a.filter.Match(req)
}

// Add records an exchange. When the recording writes as it goes, a request it has not seen before is
// turned into a mapping at once; a repeat of one it has seen adds nothing. The error is the
// callback's, and the exchange is kept either way.
func (a *Active) Add(e Exchange) error {
	a.mu.Lock()
	a.exchanges = append(a.exchanges, e)
	onNew := a.onNew
	a.mu.Unlock()
	if onNew == nil {
		return nil
	}
	built, err := Build(&a.Spec, []Exchange{e})
	if err != nil || len(built) == 0 {
		return err
	}
	key, err := json.Marshal(built[0].Request)
	if err != nil {
		return fmt.Errorf("encode recorded request: %w", err)
	}
	a.mu.Lock()
	if a.seen == nil {
		a.seen = map[string]bool{}
	}
	repeat := a.seen[string(key)]
	a.seen[string(key)] = true
	a.mu.Unlock()
	if repeat {
		return nil
	}
	return onNew(built[0])
}

// Exchanges returns what has been recorded so far.
func (a *Active) Exchanges() []Exchange {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Exchange(nil), a.exchanges...)
}

// Recorder tracks the recordings of every scope.
type Recorder struct {
	mu     sync.Mutex
	active map[store.Scope]*Active
	status map[store.Scope]string
}

// NewRecorder returns a recorder with nothing running.
func NewRecorder() *Recorder {
	return &Recorder{active: map[store.Scope]*Active{}, status: map[store.Scope]string{}}
}

// Start begins recording in a scope. filter is the compiled spec.Filters, or nil.
func (r *Recorder) Start(sc store.Scope, spec Spec, filter *match.RequestMatcher) error {
	return r.StartLive(sc, spec, filter, nil)
}

// StartLive is Start for a recording that hands each new request's mapping to onNew as it arrives.
func (r *Recorder) StartLive(sc store.Scope, spec Spec, filter *match.RequestMatcher, onNew func(model.StubMapping) error) error {
	if spec.TargetBaseURL == "" {
		return apierr.Invalid("/targetBaseUrl", "targetBaseUrl is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[sc] != nil {
		return apierr.New(http.StatusConflict, apierr.RecordingInProgress, "A recording is already running in this scope.")
	}
	r.active[sc] = &Active{Spec: spec, filter: filter, onNew: onNew}
	r.status[sc] = StatusRecording
	return nil
}

// Stop ends the recording in a scope and returns it, or nil when none was running.
func (r *Recorder) Stop(sc store.Scope) *Active {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.active[sc]
	if a != nil {
		delete(r.active, sc)
		r.status[sc] = StatusStopped
	}
	return a
}

// Status reports the state of a scope's recording.
func (r *Recorder) Status(sc store.Scope) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st := r.status[sc]; st != "" {
		return st
	}
	return StatusNever
}

// Find returns the running recording of the first scope that has one. Scopes are given most
// specific first, so a shadow mock's recording wins over the global one.
func (r *Recorder) Find(scopes []store.Scope) *Active {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sc := range scopes {
		if a := r.active[sc]; a != nil {
			return a
		}
	}
	return nil
}

// Forget drops everything the recorder knows about a scope, for when the scope goes away.
func (r *Recorder) Forget(sc store.Scope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, sc)
	delete(r.status, sc)
}

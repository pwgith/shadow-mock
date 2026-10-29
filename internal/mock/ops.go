package mock

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
)

// nearMissLimit caps near-miss lists, as WireMock does.
const nearMissLimit = 3

// CompilePattern compiles a verification pattern, reporting problems as validation errors.
func (s *Service) CompilePattern(p *model.RequestPattern) (*match.RequestMatcher, error) {
	rm, err := match.Compile(p, s.Layers.Options())
	if err != nil {
		return nil, apierr.Invalid("", "%s", err.Error())
	}
	return rm, nil
}

// FindRequests returns the journal entries in scope that match a pattern.
func (s *Service) FindRequests(sc store.Scope, rm *match.RequestMatcher) []*journal.Entry {
	return s.Journal.Query(sc, func(e *journal.Entry) bool { return rm.Match(e.Request) }, 0)
}

// RemoveRequests deletes and returns the journal entries in scope that match a pattern.
func (s *Service) RemoveRequests(sc store.Scope, rm *match.RequestMatcher) []*journal.Entry {
	return s.Journal.Remove(sc, func(e *journal.Entry) bool { return rm.Match(e.Request) })
}

// RemoveRequestsByMetadata deletes and returns the journal entries in scope that a stub with
// matching metadata served.
func (s *Service) RemoveRequestsByMetadata(sc store.Scope, p model.ValuePattern) ([]*journal.Entry, error) {
	cm, err := match.CompileContent(p, s.Layers.Options())
	if err != nil {
		return nil, apierr.Invalid("", "%s", err.Error())
	}
	return s.Journal.Remove(sc, func(e *journal.Entry) bool {
		return e.Stub != nil && metadataMatches(cm, e.Stub.Metadata)
	}), nil
}

// UnmatchedRequests returns the journal entries in scope that no stub served.
func (s *Service) UnmatchedRequests(sc store.Scope) []*journal.Entry {
	return s.Journal.Query(sc, func(e *journal.Entry) bool { return !e.Matched() }, 0)
}

// NearMissesFor ranks the stubs in the scope's stack against a request.
func (s *Service) NearMissesFor(sc store.Scope, req *match.Request) []engine.NearMiss {
	return engine.NearMisses(req, s.Stack(sc), nearMissLimit)
}

// PatternNearMiss is a journal entry that nearly matches a pattern.
type PatternNearMiss struct {
	Entry    *journal.Entry
	Distance float64
}

// NearMissesForPattern ranks journal entries by how close they come to a pattern.
func (s *Service) NearMissesForPattern(sc store.Scope, rm *match.RequestMatcher) []PatternNearMiss {
	entries := s.Journal.Query(sc, nil, 0)
	out := make([]PatternNearMiss, 0, len(entries))
	for _, e := range entries {
		out = append(out, PatternNearMiss{Entry: e, Distance: rm.Score(e.Request).Distance})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Distance < out[j].Distance })
	if len(out) > nearMissLimit {
		out = out[:nearMissLimit]
	}
	return out
}

// CounterView is a counter with the layer its stub belongs to.
type CounterView struct {
	StubID       string
	Layer        session.Kind
	Count        int64
	LastCalledAt time.Time
}

// ListCounters returns the scope's call counters.
func (s *Service) ListCounters(ctx context.Context, sc store.Scope) ([]CounterView, error) {
	es, err := s.Counters.List(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("list counters: %w", err)
	}
	out := make([]CounterView, 0, len(es))
	for _, e := range es {
		out = append(out, CounterView{StubID: e.Key.StubID, Layer: s.layerKind(e.Key.StubScope), Count: e.Count, LastCalledAt: e.LastCalledAt})
	}
	return out, nil
}

func (s *Service) layerKind(sc store.Scope) session.Kind {
	if sc.IsGlobal() {
		return session.Global
	}
	if l, ok := s.Layers.Layer(sc); ok {
		return l.Kind
	}
	return session.KeyBased
}

// stubInEffect finds the stub with the id that requests in scope sc would see: the most
// specific layer holding it, or the layer named by want.
func (s *Service) stubInEffect(sc store.Scope, id string, want session.Kind) (*engine.Layer, error) {
	for _, l := range s.Stack(sc) {
		if want != "" && l.Kind != want {
			continue
		}
		if _, ok := l.Snapshot().Stub(id); ok {
			return l, nil
		}
	}
	return nil, errNotFound("Mapping %s is not in scope.", id)
}

// SetCounter sets how many calls a stub has had in the scope. The next call gets count + 1.
func (s *Service) SetCounter(ctx context.Context, sc store.Scope, id string, count int64, layer session.Kind) (CounterView, error) {
	if count < 0 {
		return CounterView{}, apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed, "count must not be negative.")
	}
	l, err := s.stubInEffect(sc, id, layer)
	if err != nil {
		return CounterView{}, err
	}
	if err := s.Counters.Set(ctx, counter.Key{RequestScope: sc, StubScope: l.Scope, StubID: id}, count); err != nil {
		return CounterView{}, fmt.Errorf("set counter: %w", err)
	}
	return CounterView{StubID: id, Layer: l.Kind, Count: count}, nil
}

// ResetCounter zeroes one stub's counters in the scope.
func (s *Service) ResetCounter(ctx context.Context, sc store.Scope, id string) error {
	if _, err := s.stubInEffect(sc, id, ""); err != nil {
		return err
	}
	if err := s.Counters.Reset(ctx, sc, id); err != nil {
		return fmt.Errorf("reset counter: %w", err)
	}
	return nil
}

// ResetCounters zeroes every counter in the scope.
func (s *Service) ResetCounters(ctx context.Context, sc store.Scope) error {
	if err := s.Counters.Reset(ctx, sc, ""); err != nil {
		return fmt.Errorf("reset counters: %w", err)
	}
	return nil
}

// ResetAll resets the scope's mappings, journal and call counters (WireMock's /__admin/reset).
func (s *Service) ResetAll(ctx context.Context, sc store.Scope) error {
	if err := s.ResetMappings(ctx, sc); err != nil {
		return err
	}
	s.Journal.Remove(sc, nil)
	return s.ResetCounters(ctx, sc)
}

// DryRunRequest is the body of POST /__admin/mappings/dry-run.
type DryRunRequest struct {
	Request   model.SampleRequest `json:"request"`
	SessionID string              `json:"sessionId"`
	URLMockID string              `json:"urlMockId"`
}

// DryRunResult is what a request would get, without side effects.
type DryRunResult struct {
	Matched    bool
	Layer      session.Kind
	SessionID  string
	Stub       *model.StubMapping
	Status     int
	Header     http.Header
	Body       []byte
	NearMisses []engine.NearMiss
}

// DryRun resolves a sample request through a scope's stack. It never advances counters or
// writes to the journal; call-count rules use the number the next real call would get.
func (s *Service) DryRun(ctx context.Context, sc store.Scope, req DryRunRequest) (DryRunResult, error) {
	stackScope := sc
	for _, id := range []string{req.SessionID, req.URLMockID} {
		if id == "" {
			continue
		}
		if _, ok := s.Sessions.Get(sc.Tenant, id); !ok {
			return DryRunResult{}, errNotFound("No active session %s.", id)
		}
	}
	switch {
	case req.SessionID != "":
		stackScope.SessionID = req.SessionID
	case req.URLMockID != "":
		stackScope.SessionID = req.URLMockID
	}
	stack := s.Stack(stackScope)
	r := match.FromSample(&req.Request)
	res := engine.Resolve(r, stack)
	if res.Stub == nil {
		return DryRunResult{NearMisses: engine.NearMisses(r, stack, nearMissLimit)}, nil
	}
	n, err := s.Counters.Peek(ctx, counter.Key{RequestScope: stack.RequestScope(), StubScope: res.Stub.Scope, StubID: res.Stub.ID()})
	if err != nil {
		return DryRunResult{}, fmt.Errorf("peek counter: %w", err)
	}
	out, err := s.render(ctx, res, stack, r, n)
	if err != nil {
		return DryRunResult{}, err
	}
	result := DryRunResult{Matched: true, Layer: res.Layer.Kind, Stub: &res.Stub.Mapping, Status: out.Status, Header: out.Header, Body: out.Body}
	if !res.Layer.Scope.IsGlobal() {
		result.SessionID = res.Layer.Scope.SessionID
	}
	return result, nil
}

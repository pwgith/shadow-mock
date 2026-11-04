// Package mock is the Shadow Mock service. It serves mock traffic and carries out admin
// operations, keeping the stores, layers, detector, counters and journal consistent. The
// admin package translates HTTP into calls on Service; the server package routes traffic.
package mock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/detect"
	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/proxy"
	"github.com/pwgith/shadow-mock/internal/record"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/telemetry"
	"github.com/pwgith/shadow-mock/internal/tenant"
	"github.com/pwgith/shadow-mock/internal/usage"
	"github.com/pwgith/shadow-mock/internal/webhook"
)

// Config holds the service options that are not owned by a component.
type Config struct {
	// MaxBodyBytes caps request bodies (NFR-5.4).
	MaxBodyBytes int64
	// MappingSetsDir holds saved mapping sets for seeding sessions (FR-8.7).
	MappingSetsDir string
	// MatchedHeader echoes X-Shadow-Mock-Matched on mock responses (FR-7.12).
	MatchedHeader bool
	// KeepUnknownFields stores a mapping field Shadow Mock does not know instead of refusing it
	// (mappings.unknownFields: keep, FR-1.4).
	KeepUnknownFields bool
	// StubCORS answers CORS preflight requests and adds CORS headers (server.stubCors).
	StubCORS bool
	// ServeFiles serves __files over HTTP (server.serveFiles).
	ServeFiles bool
	// SaaS turns off standalone-only operations such as save and shutdown.
	SaaS bool
	// LogRequests writes a log line for every mock request (log.requests).
	LogRequests bool
	// Limits caps what a shared server holds. A zero field is no limit.
	Limits Limits
}

// Limits are the quotas the service enforces when something is created.
type Limits struct {
	MaxSessions           int
	MaxMappings           int
	MaxMappingsPerSession int
	MaxDelay              time.Duration
}

// SessionSaver persists the session registry (storage.sessions.driver: fs).
type SessionSaver interface {
	SaveSessions(sessions []session.Session) error
}

// Service is the Shadow Mock service.
type Service struct {
	// SessionStore persists sessions after every change; nil keeps them in memory only.
	SessionStore SessionSaver

	Mappings store.SplitMappings
	Files    store.FileRepository
	Sessions *session.Registry
	Layers   *engine.Manager
	Detector *detect.Index
	Counters counter.Counters
	Journal  *journal.Memory
	Renderer *respond.Renderer
	// Webhooks sends the calls mappings make after serving; nil disables them.
	Webhooks *webhook.Dispatcher
	// Proxy forwards requests to upstream servers; Recorder holds the running recordings.
	Proxy *proxy.Forwarder
	// BrowserProxy sends on the requests the browser proxy passes through. It differs from Proxy in
	// which origin certificates it trusts.
	BrowserProxy *proxy.Forwarder
	Recorder     *record.Recorder
	Stats        *stats.Tracker
	// Usage counts what each org uses (managed mode); nil counts nothing.
	Usage *usage.Meter
	// Orgs finds an org's plan limits and settings (managed mode); nil means the server's own settings apply to all.
	Orgs OrgSource
	// Metrics receives one call per served request; nil disables it.
	Metrics Observer
	// Telemetry starts a span for each mock request; nil records nothing.
	Telemetry *telemetry.Telemetry
	Clock     clock.Clock
	Logger    *slog.Logger
	Config    Config

	inflight sync.Map // path -> *atomic.Int64
}

// errNotFound is a 404 with the not_found code.
func errNotFound(format string, a ...any) error {
	return apierr.New(http.StatusNotFound, apierr.NotFound, format, a...)
}

// storeErr turns a store.ErrNotFound into a 404 and wraps anything else.
func storeErr(err error, what string) error {
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("%s was not found.", what)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Start loads the global layer of each tenant and rebuilds layers when the store reports
// external changes, until ctx is done.
func (s *Service) Start(ctx context.Context, tenants []tenant.ID, changes <-chan store.ChangeEvent) error {
	for _, t := range tenants {
		if err := s.Layers.Rebuild(ctx, store.Global(t)); err != nil {
			return err
		}
	}
	if changes == nil {
		return nil
	}
	go func() {
		for ev := range changes {
			if err := s.Layers.Rebuild(ctx, ev.Scope); err != nil {
				s.Logger.ErrorContext(ctx, "rebuild after change", "scope", ev.Scope.String(), "error", err.Error())
			}
		}
	}()
	return nil
}

// persist saves the registry if sessions are persisted. A failure is logged, not returned:
// the change has already taken effect in memory.
func (s *Service) persist(ctx context.Context) {
	if s.SessionStore == nil {
		return
	}
	if err := s.SessionStore.SaveSessions(s.Sessions.All()); err != nil {
		s.Logger.ErrorContext(ctx, "persist sessions", "error", err.Error())
	}
}

// RestoreSessions loads persisted sessions into the registry and builds their layers.
func (s *Service) RestoreSessions(ctx context.Context, sessions []session.Session) error {
	s.Sessions.Restore(sessions)
	for _, sess := range s.Sessions.All() {
		if err := s.Layers.Rebuild(ctx, sess.Scope()); err != nil {
			return err
		}
	}
	s.Detector.Invalidate()
	return nil
}

// Stack returns the layer stack for a scope: the session, its URL-based parent if it is
// nested in one, then global.
func (s *Service) Stack(sc store.Scope) engine.Stack {
	if sc.IsGlobal() {
		return s.Layers.Stack(sc.Tenant)
	}
	ids := []string{sc.SessionID}
	if sess, ok := s.Sessions.Get(sc.Tenant, sc.SessionID); ok && sess.ParentID != "" {
		ids = append(ids, sess.ParentID)
	}
	return s.stackOf(sc.Tenant, ids)
}

// stackOf builds the stack for the sessions (most specific first). The global layer ends it, unless one of the
// sessions was created with inheritGlobal=false (FR-7.13).
func (s *Service) stackOf(t tenant.ID, sessionIDs []string) engine.Stack {
	st := s.Layers.Stack(t, sessionIDs...)
	if s.skipsGlobal(t, sessionIDs) {
		return st[:len(st)-1]
	}
	return st
}

// skipsGlobal reports whether any of the sessions keeps requests from reaching the global layer.
func (s *Service) skipsGlobal(t tenant.ID, sessionIDs []string) bool {
	return slices.ContainsFunc(sessionIDs, func(id string) bool {
		sess, ok := s.Sessions.Get(t, id)
		return ok && sess.NoGlobal
	})
}

// nearMisses ranks the closest mappings for a request that stack did not serve. When the stack leaves the global layer
// out, the global layer's mappings are still ranked, marked as skipped, so a miss can be traced to a mapping the
// session chose not to inherit. They are only a hint; they never serve a response.
func (s *Service) nearMisses(req *match.Request, stack engine.Stack, limit int) []engine.NearMiss {
	if len(stack) == 0 || stack[len(stack)-1].Kind == session.Global {
		return engine.NearMisses(req, stack, limit)
	}
	global, _ := s.Layers.Layer(store.Global(stack[0].Scope.Tenant))
	nms := engine.NearMisses(req, append(slices.Clone(stack), global), limit)
	for i := range nms {
		nms[i].Skipped = nms[i].Layer == global
	}
	return nms
}

// fileScopes lists the scopes a stack looks up body files in, most specific first.
func fileScopes(st engine.Stack) []store.Scope {
	out := make([]store.Scope, 0, len(st))
	for _, l := range st {
		out = append(out, l.Scope)
	}
	return out
}

// RequireSession returns the session of a session scope, or a 404.
func (s *Service) RequireSession(sc store.Scope) (session.Session, error) {
	sess, ok := s.Sessions.Get(sc.Tenant, sc.SessionID)
	if !ok {
		return session.Session{}, errNotFound("No active session %s.", sc.SessionID)
	}
	return sess, nil
}

// Observer receives the outcome of every served mock request.
type Observer interface {
	Request(org, method string, status int, layer, stage string)
}

// OrgSource finds an org by id, for the plan limits and settings the engine applies itself.
type OrgSource interface {
	ByID(id tenant.ID) (*tenant.Org, bool)
}

// org returns the org with the id, or nil when the engine has no directory of orgs.
func (s *Service) org(t tenant.ID) *tenant.Org {
	if s.Orgs == nil {
		return nil
	}
	o, _ := s.Orgs.ByID(t)
	return o
}

// orgSettings returns the org's own settings, which are the defaults when the org is not known.
func (s *Service) orgSettings(t tenant.ID) tenant.Settings {
	if o := s.org(t); o != nil {
		return o.Settings
	}
	return tenant.Settings{}
}

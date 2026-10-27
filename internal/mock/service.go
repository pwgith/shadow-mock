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

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/detect"
	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
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
	// SaaS turns off standalone-only operations such as save and shutdown.
	SaaS bool
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
	Stats    *stats.Tracker
	Clock    clock.Clock
	Logger   *slog.Logger
	Config   Config
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
	return s.Layers.Stack(sc.Tenant, ids...)
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

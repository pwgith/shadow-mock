package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Manager owns the layers. It rebuilds a layer's snapshot from the store after a change, off
// the request path, and swaps it in (DESIGN.md §6.1).
type Manager struct {
	mappings store.MappingRepository
	sessions *session.Registry
	opts     match.Options
	logger   *slog.Logger

	mu     sync.RWMutex
	layers map[store.Scope]*Layer
}

// NewManager returns a manager with no layers built yet.
func NewManager(mappings store.MappingRepository, sessions *session.Registry, o match.Options, logger *slog.Logger) *Manager {
	return &Manager{mappings: mappings, sessions: sessions, opts: o, logger: logger, layers: map[store.Scope]*Layer{}}
}

// Options returns the matcher options used to compile stubs.
func (m *Manager) Options() match.Options { return m.opts }

// Rebuild recompiles the layer of scope s from the store. Invalid stored mappings are logged
// and left out; the admin API never stores one, but files may contain them.
func (m *Manager) Rebuild(ctx context.Context, s store.Scope) error {
	mappings, err := m.mappings.List(ctx, s)
	if err != nil {
		return fmt.Errorf("rebuild %s: %w", s, err)
	}
	kind := session.Global
	var masks []model.Mask
	if !s.IsGlobal() {
		sess, ok := m.sessions.Get(s.Tenant, s.SessionID)
		if !ok {
			m.Drop(s)
			return nil
		}
		kind, masks = sess.Kind, sess.Masks
	}
	snap, errs := BuildSnapshot(s, kind, mappings, masks, m.opts)
	for _, e := range errs {
		m.logger.WarnContext(ctx, "mapping not loaded", "scope", s.String(), "error", e.Error())
	}
	m.layer(s, kind).snap.Store(snap)
	return nil
}

func (m *Manager) layer(s store.Scope, kind session.Kind) *Layer {
	m.mu.RLock()
	l := m.layers[s]
	m.mu.RUnlock()
	if l != nil {
		return l
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if l = m.layers[s]; l == nil {
		l = &Layer{Scope: s, Kind: kind}
		empty, _ := BuildSnapshot(s, kind, nil, nil, m.opts)
		l.snap.Store(empty)
		m.layers[s] = l
	}
	return l
}

// Drop forgets a session's layer.
func (m *Manager) Drop(s store.Scope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.layers, s)
}

// Layer returns the layer of scope s. The global layer always exists; a session layer exists
// once the session has been built.
func (m *Manager) Layer(s store.Scope) (*Layer, bool) {
	if s.IsGlobal() {
		return m.layer(s, session.Global), true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.layers[s]
	return l, ok
}

// Stack builds the stack for a request: the given sessions (most specific first) and then
// the tenant's global layer. Unknown sessions are skipped.
func (m *Manager) Stack(t tenant.ID, sessionIDs ...string) Stack {
	st := make(Stack, 0, len(sessionIDs)+1)
	for _, id := range sessionIDs {
		if id == "" {
			continue
		}
		if l, ok := m.Layer(store.Scope{Tenant: t, SessionID: id}); ok {
			st = append(st, l)
		}
	}
	return append(st, m.layer(store.Global(t), session.Global))
}

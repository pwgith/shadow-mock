package session

import (
	"context"
	"crypto/subtle"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/ids"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Registry holds active sessions in memory. Expired sessions are invisible even before the
// reaper removes them, so expiry never depends on the reaper's timing.
type Registry struct {
	clock  clock.Clock
	limits Limits

	mu       sync.RWMutex
	sessions map[tenant.ID]map[string]*Session
}

// NewRegistry returns an empty registry.
func NewRegistry(c clock.Clock, l Limits) *Registry {
	return &Registry{clock: c, limits: l, sessions: map[tenant.ID]map[string]*Session{}}
}

// Restore adds sessions loaded from a store at start-up. Expired ones are skipped.
func (r *Registry) Restore(sessions []Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	for i := range sessions {
		s := sessions[i].clone()
		if s.Expired(now) {
			continue
		}
		if r.sessions[s.Tenant] == nil {
			r.sessions[s.Tenant] = map[string]*Session{}
		}
		r.sessions[s.Tenant][s.ID] = &s
	}
}

// Limits returns the registry's rules.
func (r *Registry) Limits() Limits { return r.limits }

// CreateRequest is the validated input to Create.
type CreateRequest struct {
	Kind       Kind
	Name       string
	TTLSeconds *int64
	Tokens     []string
	ParentID   string
	Masks      []model.Mask
	Metadata   map[string]any
	CreatedBy  string
	// CreatedByKey is the label of the Tool app key that made the session.
	CreatedByKey string
}

// Create validates and registers a new session.
func (r *Registry) Create(_ context.Context, t tenant.ID, req CreateRequest) (Session, error) {
	now := r.clock.Now().UTC()
	ttl, err := r.checkTTL(req.Kind, req.TTLSeconds)
	if err != nil {
		return Session{}, err
	}
	s := Session{
		Kind: req.Kind, Tenant: t, Name: req.Name, ParentID: req.ParentID,
		CreatedBy: req.CreatedBy, CreatedByKey: req.CreatedByKey, CreatedAt: now, ExpiresAt: now.Add(ttl), TTL: ttl,
		Masks: slices.Clone(req.Masks), Metadata: req.Metadata,
	}
	if req.Kind == KeyBased {
		s.ID = ids.NewSessionID()
		s.Key = ids.NewSessionKey(r.limits.KeyPrefix)
		s.Tokens = slices.Clone(req.Tokens)
	} else {
		s.ID = ids.NewURLMockID(r.limits.URLMockIDLength)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkTokensLocked(t, "", s.Tokens); err != nil {
		return Session{}, err
	}
	if s.ParentID != "" {
		parent := r.getLocked(t, s.ParentID, now)
		if parent == nil || parent.Kind != URLBased {
			return Session{}, apierr.New(http.StatusUnprocessableEntity, apierr.ParentNotFound,
				"Parent %s is not an active URL-based shadow mock.", s.ParentID)
		}
		// A nested session never outlives its parent (FR-8.5).
		if s.ExpiresAt.After(parent.ExpiresAt) {
			s.ExpiresAt = parent.ExpiresAt
		}
	}
	if r.sessions[t] == nil {
		r.sessions[t] = map[string]*Session{}
	}
	stored := s.clone()
	r.sessions[t][s.ID] = &stored
	return s, nil
}

func (r *Registry) checkTTL(k Kind, seconds *int64) (time.Duration, error) {
	def, maxTTL := r.limits.ttl(k)
	if seconds == nil {
		return def, nil
	}
	if *seconds <= 0 {
		return 0, apierr.New(http.StatusUnprocessableEntity, apierr.TTLMustBePositive, "ttlSeconds must be positive.")
	}
	ttl := time.Duration(*seconds) * time.Second
	if ttl > maxTTL {
		return 0, apierr.New(http.StatusUnprocessableEntity, apierr.TTLExceedsMaximum,
			"ttlSeconds %d exceeds the maximum of %d for %s shadow mocks.", *seconds, int64(maxTTL/time.Second), k)
	}
	return ttl, nil
}

// checkTokensLocked enforces the minimum length and uniqueness among the tenant's active
// sessions (FR-7.9). except is a session id whose own tokens do not count as conflicts.
func (r *Registry) checkTokensLocked(t tenant.ID, except string, tokens []string) error {
	now := r.clock.Now()
	for _, tok := range tokens {
		if len(tok) < r.limits.MinTokenLength {
			return apierr.New(http.StatusUnprocessableEntity, apierr.TokenTooShort,
				"Match token %q is shorter than the minimum of %d characters.", tok, r.limits.MinTokenLength)
		}
		for id, s := range r.sessions[t] {
			if id == except || s.Expired(now) {
				continue
			}
			if slices.Contains(s.Tokens, tok) || s.Key == tok {
				return apierr.New(http.StatusConflict, apierr.TokenInUse,
					"Match token %q is already used by session %s.", tok, id)
			}
		}
	}
	return nil
}

func (r *Registry) getLocked(t tenant.ID, id string, now time.Time) *Session {
	s := r.sessions[t][id]
	if s == nil || s.Expired(now) {
		return nil
	}
	return s
}

// Get returns an active session by id.
func (r *Registry) Get(t tenant.ID, id string) (Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := r.getLocked(t, id, r.clock.Now())
	if s == nil {
		return Session{}, false
	}
	return s.clone(), true
}

// Resolve finds an active session by id or by session key, as the X-Shadow-Mock-Key admin
// header allows (FR-7.3). Keys are compared in constant time.
func (r *Registry) Resolve(t tenant.ID, keyOrID string) (Session, bool) {
	if s, ok := r.Get(t, keyOrID); ok {
		return s, true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := r.clock.Now()
	for _, s := range r.sessions[t] {
		if s.Key != "" && !s.Expired(now) && subtle.ConstantTimeCompare([]byte(s.Key), []byte(keyOrID)) == 1 {
			return s.clone(), true
		}
	}
	return Session{}, false
}

// List returns the tenant's active sessions, newest first, optionally of one kind.
func (r *Registry) List(t tenant.ID, kind Kind) []Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := r.clock.Now()
	var out []Session
	for _, s := range r.sessions[t] {
		if s.Expired(now) || (kind != "" && s.Kind != kind) {
			continue
		}
		out = append(out, s.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ListAll returns all the tenant's sessions, expired ones that have not been removed yet too, newest first.
func (r *Registry) ListAll(t tenant.ID) []Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Session, 0, len(r.sessions[t]))
	for _, s := range r.sessions[t] {
		out = append(out, s.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Anonymise replaces a person as the creator of the tenant's sessions with marker, and returns how many it changed.
func (r *Registry) Anonymise(t tenant.ID, person, marker string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions[t] {
		if s.CreatedBy == person {
			s.CreatedBy = marker
			n++
		}
	}
	return n
}

// All returns every active session of every tenant, for building detection indexes.
func (r *Registry) All() []Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := r.clock.Now()
	var out []Session
	for _, byID := range r.sessions {
		for _, s := range byID {
			if !s.Expired(now) {
				out = append(out, s.clone())
			}
		}
	}
	return out
}

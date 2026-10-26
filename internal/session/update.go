package session

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Patch is a session update (SessionPatch in the API).
type Patch struct {
	Name         *string        `json:"name"`
	TTLSeconds   *int64         `json:"ttlSeconds"`
	AddTokens    []string       `json:"addTokens"`
	RemoveTokens []string       `json:"removeTokens"`
	AddMasks     []model.Mask   `json:"addMasks"`
	RemoveMasks  []model.Mask   `json:"removeMasks"`
	Metadata     map[string]any `json:"metadata"`
}

func notFound(id string) error {
	return apierr.New(http.StatusNotFound, apierr.NotFound, "No active session %s.", id)
}

// Update applies a patch. A new TTL counts from now, within the kind's maximum.
func (r *Registry) Update(_ context.Context, t tenant.ID, id string, p Patch) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now().UTC()
	s := r.getLocked(t, id, now)
	if s == nil {
		return Session{}, notFound(id)
	}
	next := s.clone()
	if p.TTLSeconds != nil {
		ttl, err := r.checkTTL(s.Kind, p.TTLSeconds)
		if err != nil {
			return Session{}, err
		}
		next.TTL = ttl
		next.ExpiresAt = r.capToParentLocked(t, &next, now.Add(ttl), now)
	}
	if len(p.AddTokens) > 0 {
		if s.Kind != KeyBased {
			return Session{}, apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed,
				"Only key-based shadow mocks have match tokens.")
		}
		if err := r.checkTokensLocked(t, id, p.AddTokens); err != nil {
			return Session{}, err
		}
		for _, tok := range p.AddTokens {
			if !slices.Contains(next.Tokens, tok) {
				next.Tokens = append(next.Tokens, tok)
			}
		}
	}
	next.Tokens = slices.DeleteFunc(next.Tokens, func(tok string) bool { return slices.Contains(p.RemoveTokens, tok) })
	next.Masks = append(next.Masks, p.AddMasks...)
	next.Masks = slices.DeleteFunc(next.Masks, func(m model.Mask) bool {
		return slices.ContainsFunc(p.RemoveMasks, m.Equal)
	})
	if p.Name != nil {
		next.Name = *p.Name
	}
	if p.Metadata != nil {
		next.Metadata = p.Metadata
	}
	*s = next
	return next.clone(), nil
}

// KeepAlive extends the TTL by its original duration, counted from now.
func (r *Registry) KeepAlive(_ context.Context, t tenant.ID, id string) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now().UTC()
	s := r.getLocked(t, id, now)
	if s == nil {
		return Session{}, notFound(id)
	}
	s.ExpiresAt = r.capToParentLocked(t, s, now.Add(s.TTL), now)
	return s.clone(), nil
}

func (r *Registry) capToParentLocked(t tenant.ID, s *Session, at, now time.Time) time.Time {
	if s.ParentID == "" {
		return at
	}
	if parent := r.getLocked(t, s.ParentID, now); parent != nil && at.After(parent.ExpiresAt) {
		return parent.ExpiresAt
	}
	return at
}

// Remove deletes a session and any key-based sessions nested in it, and returns their scopes
// so the caller can delete what they own. It fails if the session does not exist.
func (r *Registry) Remove(_ context.Context, t tenant.ID, id string) ([]store.Scope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getLocked(t, id, r.clock.Now()) == nil {
		return nil, notFound(id)
	}
	return r.removeLocked(t, id), nil
}

func (r *Registry) removeLocked(t tenant.ID, id string) []store.Scope {
	scopes := []store.Scope{{Tenant: t, SessionID: id}}
	delete(r.sessions[t], id)
	for childID, c := range r.sessions[t] {
		if c.ParentID == id {
			scopes = append(scopes, r.removeLocked(t, childID)...)
		}
	}
	return scopes
}

// RemoveExpired deletes every expired session and returns their scopes.
func (r *Registry) RemoveExpired(_ context.Context) []store.Scope {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	var scopes []store.Scope
	for t, byID := range r.sessions {
		for id, s := range byID {
			if s.Expired(now) {
				scopes = append(scopes, r.removeLocked(t, id)...)
			}
		}
	}
	return scopes
}

// String describes a session for logs, without its key.
func (s *Session) String() string { return fmt.Sprintf("%s %s (%s)", s.Kind, s.ID, s.Tenant) }

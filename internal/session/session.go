// Package session manages key-based and URL-based shadow mocks: creation rules, TTLs, match
// tokens and the in-memory registry (DESIGN.md §4.3, FR-7, FR-8).
package session

import (
	"slices"
	"time"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Kind is the kind of session.
type Kind string

// Session kinds, and the global layer's kind.
const (
	Global   Kind = "global"
	KeyBased Kind = "key-based"
	URLBased Kind = "url-based"
)

// Session is a key-based or URL-based shadow mock.
type Session struct {
	// ID is "ses_…" for key-based sessions, and the URL-based mock id for URL-based ones.
	ID     string
	Kind   Kind
	Tenant tenant.ID
	// Key is the key-based session key. It is returned once, at creation.
	Key string
	// Tokens are extra match tokens (key-based only, FR-7.9).
	Tokens []string
	// ParentID is the URL-based shadow mock a key-based one is nested in ("" = global).
	ParentID  string
	Name      string
	CreatedBy string
	// CreatedByKey is the label of the Tool app key that made the session, when one did (FR-11.3).
	CreatedByKey string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	// TTL is the session's lifetime, used by keepalive.
	TTL      time.Duration
	Masks    []model.Mask
	Metadata map[string]any
}

// Scope returns the session's storage scope.
func (s *Session) Scope() store.Scope { return store.Scope{Tenant: s.Tenant, SessionID: s.ID} }

// Expired reports whether the session has expired at now.
func (s *Session) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }

// KeyPrefix returns the first 8 characters of the key, which identify it in listings.
func (s *Session) KeyPrefix() string {
	if len(s.Key) < 8 {
		return s.Key
	}
	return s.Key[:8]
}

// clone returns a deep enough copy for the registry to hand out safely.
func (s Session) clone() Session {
	s.Tokens = slices.Clone(s.Tokens)
	s.Masks = slices.Clone(s.Masks)
	return s
}

// Limits are the TTL and token rules (FR-7.2, FR-7.9).
type Limits struct {
	KeyBasedDefaultTTL time.Duration
	KeyBasedMaxTTL     time.Duration
	URLBasedDefaultTTL time.Duration
	URLBasedMaxTTL     time.Duration
	MinTokenLength     int
	KeyPrefix          string
	URLMockIDLength    int
}

// DefaultLimits are the documented defaults.
func DefaultLimits() Limits {
	return Limits{
		KeyBasedDefaultTTL: time.Hour,
		KeyBasedMaxTTL:     24 * time.Hour,
		URLBasedDefaultTTL: 24 * time.Hour,
		URLBasedMaxTTL:     30 * 24 * time.Hour,
		MinTokenLength:     6,
		KeyPrefix:          "smk_",
		URLMockIDLength:    16,
	}
}

func (l Limits) ttl(k Kind) (def, maxTTL time.Duration) {
	if k == URLBased {
		return l.URLBasedDefaultTTL, l.URLBasedMaxTTL
	}
	return l.KeyBasedDefaultTTL, l.KeyBasedMaxTTL
}

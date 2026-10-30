package auth

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/clock"
)

// Scopes a token can carry (FR-11.2, FR-11.4, FR-11.15).
const (
	// ScopeMetadata limits a token to metadata: a support "view as" session.
	ScopeMetadata = "metadata"
	// ScopeSystem marks the management app's own calls: purging an org, anonymising a person.
	ScopeSystem = "system"
)

// DefaultMaxLifetime is the longest a token may live, whatever its issuer says.
const DefaultMaxLifetime = 2 * time.Minute

// clockSkew is the leeway given to the issuer's clock.
const clockSkew = 5 * time.Second

// Kind says which credential a principal came from.
type Kind string

// The credential kinds.
const (
	KindToken Kind = "token"
	KindKey   Kind = "key"
)

// Issuer is a trusted source of tokens. Trust comes from configuration only (FR-11.2).
type Issuer struct {
	// Name labels the issuer in the audit trail.
	Name string
	// Issuer is the exact `iss` the issuer writes; Audience the exact `aud` this environment expects.
	Issuer   string
	Audience string
	// Keys finds the key a token was signed with.
	Keys KeySource
	// MaxLifetime caps the lifetime of a token; zero is DefaultMaxLifetime.
	MaxLifetime time.Duration
	// RoleMap maps an `app_role` claim to a role. Empty uses admin, member and viewer.
	RoleMap map[string]Role
	// ReplayCheck makes each token usable once.
	ReplayCheck bool
}

func (i Issuer) maxLifetime() time.Duration {
	if i.MaxLifetime > 0 {
		return i.MaxLifetime
	}
	return DefaultMaxLifetime
}

func (i Issuer) role(claim string) (Role, bool) {
	m := i.RoleMap
	if len(m) == 0 {
		m = map[string]Role{"admin": Admin, "member": Editor, "viewer": Viewer}
	}
	r, ok := m[claim]
	return r, ok
}

// Replay remembers token ids until they expire, so a token is used once.
type Replay interface {
	// Seen reports whether the id was used before, and remembers it until `until`.
	Seen(issuer, id string, until time.Time) bool
}

// MemoryReplay is a Replay held in memory.
type MemoryReplay struct {
	clk  clock.Clock
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewMemoryReplay returns an empty replay cache.
func NewMemoryReplay(clk clock.Clock) *MemoryReplay {
	return &MemoryReplay{clk: clk, seen: map[string]time.Time{}}
}

// Seen implements Replay.
func (m *MemoryReplay) Seen(issuer, id string, until time.Time) bool {
	now := m.clk.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, exp := range m.seen {
		if exp.Before(now) {
			delete(m.seen, k)
		}
	}
	key := issuer + "\x00" + id
	if _, ok := m.seen[key]; ok {
		return true
	}
	m.seen[key] = until
	return false
}

// Managed is the managed-mode authenticator: a token from a trusted issuer, or a Tool app key, and
// nothing else (FR-9.3a).
type Managed struct {
	issuers []Issuer
	keys    KeyStore
	replay  Replay
	clk     clock.Clock
}

var _ Authenticator = (*Managed)(nil)

// ManagedOptions are what NewManaged needs.
type ManagedOptions struct {
	Issuers []Issuer
	Keys    KeyStore
	// Replay remembers token ids for the issuers that ask for a replay check.
	Replay Replay
	Clock  clock.Clock
}

// NewManaged returns the authenticator. Its key lookups are cached for a minute.
func NewManaged(o ManagedOptions) *Managed {
	m := &Managed{issuers: o.Issuers, replay: o.Replay, clk: o.Clock}
	if o.Keys != nil {
		m.keys = &cachedKeys{inner: o.Keys, clk: o.Clock, cache: map[[sha256.Size]byte]cacheEntry{}}
	}
	return m
}

// Authenticate reads the bearer credential and yields the principal. It never gives a reason a key
// failed beyond "invalid".
func (m *Managed) Authenticate(r *http.Request) (Principal, error) {
	cred, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidToken,
			"The admin API needs a token or a Tool app key: send it as 'Authorization: Bearer <credential>'.")
	}
	switch {
	case strings.HasPrefix(cred, mockSetKeyPrefix):
		return Principal{}, invalid() // a mock set key belongs to the management app and is never looked up here
	case strings.HasPrefix(cred, keyPrefix):
		return m.authenticateKey(r, cred)
	}
	return m.authenticateToken(r, cred)
}

func invalid() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, apierr.InvalidToken, "The credential is not valid.")
}

func (m *Managed) authenticateKey(r *http.Request, cred string) (Principal, error) {
	if m.keys == nil || !keyShape.MatchString(cred) {
		return Principal{}, invalid()
	}
	hash := HashKey(cred)
	k, ok := m.keys.Lookup(hash)
	now := m.clk.Now()
	if !ok || (!k.Expires.IsZero() && !k.Expires.After(now)) {
		return Principal{}, invalid()
	}
	m.keys.Touch(hash, clientAddr(r), now)
	return Principal{
		UserID: k.Label, OrgID: k.Org, Roles: []Role{Editor}, Kind: KindKey, KeyLabel: k.Label, KeyScopes: k.Scopes,
	}, nil
}

// clientAddr is the address the caller came from, for the key-usage record: the one the server
// resolved and put in the context, else the peer.
func clientAddr(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(netip.Addr); ok {
		return ip.String()
	}
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().String()
	}
	return r.RemoteAddr
}

type clientIPKey struct{}

// WithClientIP records the resolved client address for the key-usage record.
func WithClientIP(ctx context.Context, ip netip.Addr) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

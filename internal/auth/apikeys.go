package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Tool app key scopes (FR-11.3).
const (
	ScopeSessions = "sessions"
	ScopeValidate = "validate"
	ScopeRead     = "read"
)

const (
	// keyPrefix starts a Tool app key; mockSetKeyPrefix starts a mock set key, which belongs to the
	// management app and is never accepted here.
	keyPrefix        = "smt_"
	mockSetKeyPrefix = "smk_"
	// keyCacheTTL is how long a lookup is trusted, so a revoked key stops within a minute.
	keyCacheTTL = time.Minute
	// usageGap is the least time between recordings of a key's use.
	usageGap = time.Minute
)

// keyShape is smt_<16 base32 id>_<52 base32 secret>.
var keyShape = regexp.MustCompile(`^smt_[A-Z2-7]{16}_[A-Z2-7]{52}$`)

// APIKey is a Tool app key as the engine knows it. The secret itself is not kept, only its hash.
type APIKey struct {
	Hash    [sha256.Size]byte
	Org     tenant.ID
	Label   string
	Scopes  []string
	Expires time.Time // zero: never
}

// Has reports whether the key has the scope.
func (k APIKey) Has(scope string) bool {
	for _, s := range k.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// KeyStore finds Tool app keys by the hash of the whole key.
type KeyStore interface {
	// Lookup returns the key with the hash, comparing in constant time.
	Lookup(ctx context.Context, hash [sha256.Size]byte) (APIKey, bool)
	// Touch records that a key was used, from a source address.
	Touch(ctx context.Context, hash [sha256.Size]byte, ip string, at time.Time)
}

// KeyUse is when and from where a key was last used.
type KeyUse struct {
	At time.Time
	IP string
}

// MemoryKeys is a KeyStore held in memory. The management app's database table replaces it.
type MemoryKeys struct {
	mu      sync.RWMutex
	keys    []APIKey
	used    map[[sha256.Size]byte]KeyUse
	lookups atomic.Int64
}

var _ KeyStore = (*MemoryKeys)(nil)

// NewMemoryKeys returns an empty store.
func NewMemoryKeys() *MemoryKeys { return &MemoryKeys{used: map[[sha256.Size]byte]KeyUse{}} }

// HashKey returns the hash a store holds for a key.
func HashKey(key string) [sha256.Size]byte { return sha256.Sum256([]byte(key)) }

// Add stores a key.
func (m *MemoryKeys) Add(k APIKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys = append(m.keys, k)
}

// Revoke removes the key with the hash.
func (m *MemoryKeys) Revoke(hash [sha256.Size]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.keys {
		if subtle.ConstantTimeCompare(m.keys[i].Hash[:], hash[:]) == 1 {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return
		}
	}
}

// Lookup compares the hash with every stored key, so the time taken does not say which was close.
func (m *MemoryKeys) Lookup(_ context.Context, hash [sha256.Size]byte) (APIKey, bool) {
	m.lookups.Add(1)
	m.mu.RLock()
	defer m.mu.RUnlock()
	var found APIKey
	ok := false
	for _, k := range m.keys {
		if subtle.ConstantTimeCompare(k.Hash[:], hash[:]) == 1 {
			found, ok = k, true
		}
	}
	return found, ok
}

// Lookups is how many times a key has been looked up, for tests that check a credential is refused on sight.
func (m *MemoryKeys) Lookups() int64 { return m.lookups.Load() }

// Touch records a use, at most once a minute for each key.
func (m *MemoryKeys) Touch(_ context.Context, hash [sha256.Size]byte, ip string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.used[hash]; ok && at.Sub(last.At) < usageGap {
		return
	}
	m.used[hash] = KeyUse{At: at, IP: ip}
}

// LastUse returns when and from where the key was last used.
func (m *MemoryKeys) LastUse(hash [sha256.Size]byte) (KeyUse, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.used[hash]
	return u, ok
}

// cachedKeys remembers lookups for a minute, found or not, so a store behind a database is not asked on
// every call.
type cachedKeys struct {
	inner KeyStore
	clk   clock.Clock

	mu    sync.Mutex
	cache map[[sha256.Size]byte]cacheEntry
}

type cacheEntry struct {
	key   APIKey
	found bool
	at    time.Time
}

// maxCachedKeys bounds the cache against a flood of made-up keys.
const maxCachedKeys = 4096

// Lookup answers from the cache for up to keyCacheTTL, so a revoked key stops within a minute.
func (c *cachedKeys) Lookup(ctx context.Context, hash [sha256.Size]byte) (APIKey, bool) {
	now := c.clk.Now()
	c.mu.Lock()
	if e, ok := c.cache[hash]; ok && now.Sub(e.at) < keyCacheTTL {
		c.mu.Unlock()
		return e.key, e.found
	}
	c.mu.Unlock()
	k, ok := c.inner.Lookup(ctx, hash)
	c.mu.Lock()
	if len(c.cache) >= maxCachedKeys {
		clear(c.cache)
	}
	c.cache[hash] = cacheEntry{key: k, found: ok, at: now}
	c.mu.Unlock()
	return k, ok
}

// Touch passes the use on to the store.
func (c *cachedKeys) Touch(ctx context.Context, hash [sha256.Size]byte, ip string, at time.Time) {
	c.inner.Touch(ctx, hash, ip, at)
}

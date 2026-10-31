package postgres

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// keyTimeout bounds a key lookup, which is on the authentication path.
const keyTimeout = 2 * time.Second

// touchEvery is how often a key's last use is written, whatever the traffic.
const touchEvery = time.Minute

// Keys is the store of Tool app keys, read from the database. A lookup is by the SHA-256 of the key, which is
// not the secret, so an index lookup leaks nothing useful; the caller caches answers for a minute (auth.NewManaged).
type Keys struct {
	db      *DB
	onError func(error)
	mu      sync.Mutex
	touched map[[sha256.Size]byte]time.Time
}

var _ auth.KeyStore = (*Keys)(nil)

// Keys returns the key store. onError, when set, is told when the database fails; a key lookup that fails finds
// nothing, so the request is refused rather than let in.
func (d *DB) Keys(onError func(error)) *Keys {
	return &Keys{db: d, onError: onError, touched: map[[sha256.Size]byte]time.Time{}}
}

// Lookup implements auth.KeyStore. A revoked key is not found.
func (k *Keys) Lookup(ctx context.Context, hash [sha256.Size]byte) (auth.APIKey, bool) {
	ctx, cancel := context.WithTimeout(ctx, keyTimeout)
	defer cancel()
	var (
		key     = auth.APIKey{Hash: hash}
		org     string
		expires *time.Time
	)
	err := k.db.pool.QueryRow(ctx,
		"SELECT org_id, label, scopes, expires_at FROM engine.api_keys WHERE hash = $1 AND revoked_at IS NULL", hash[:],
	).Scan(&org, &key.Label, &key.Scopes, &expires)
	if err != nil {
		if !notFound(err) && k.onError != nil {
			k.onError(err)
		}
		return auth.APIKey{}, false
	}
	key.Org = tenant.ID(org)
	if expires != nil {
		key.Expires = *expires
	}
	return key, true
}

// Touch implements auth.KeyStore. It writes at most once a minute for each key.
func (k *Keys) Touch(ctx context.Context, hash [sha256.Size]byte, ip string, at time.Time) {
	k.mu.Lock()
	if last, ok := k.touched[hash]; ok && at.Sub(last) < touchEvery {
		k.mu.Unlock()
		return
	}
	k.touched[hash] = at
	k.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, keyTimeout)
	defer cancel()
	if _, err := k.db.pool.Exec(ctx, "UPDATE engine.api_keys SET last_used_at = $2, last_used_ip = $3 WHERE hash = $1", hash[:], at, ip); err != nil && k.onError != nil {
		k.onError(err)
	}
}

// LastUse returns when and from where a key was last used.
func (k *Keys) LastUse(ctx context.Context, hash [sha256.Size]byte) (auth.KeyUse, bool, error) {
	var (
		at *time.Time
		ip *string
	)
	err := k.db.pool.QueryRow(ctx, "SELECT last_used_at, last_used_ip FROM engine.api_keys WHERE hash = $1", hash[:]).Scan(&at, &ip)
	switch {
	case notFound(err):
		return auth.KeyUse{}, false, nil
	case err != nil:
		return auth.KeyUse{}, false, err
	case at == nil:
		return auth.KeyUse{}, false, nil
	}
	use := auth.KeyUse{At: *at}
	if ip != nil {
		use.IP = *ip
	}
	return use, true, nil
}

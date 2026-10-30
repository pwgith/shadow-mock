package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
)

// KeySource finds the public key an issuer signed a token with.
type KeySource interface {
	// Key returns the key named by kid. A source with a single pinned key ignores kid.
	Key(ctx context.Context, kid string) (crypto.PublicKey, error)
}

// Pinned is a KeySource with one key.
type Pinned struct{ PublicKey crypto.PublicKey }

// Key returns the pinned key.
func (p Pinned) Key(context.Context, string) (crypto.PublicKey, error) { return p.PublicKey, nil }

const (
	// jwksTTL is how long a key set is trusted before it is read again, so rotation needs no change.
	jwksTTL = 5 * time.Minute
	// jwksMinGap is the least time between reads when a token names a key the set lacks.
	jwksMinGap = 15 * time.Second
	// jwksMaxBytes bounds a key set document.
	jwksMaxBytes = 1 << 20
)

// JWKS is a KeySource that reads a key set from a URL and caches it.
type JWKS struct {
	url    string
	client *http.Client
	clk    clock.Clock

	mu      sync.Mutex
	keys    map[string]crypto.PublicKey
	fetched time.Time
}

// NewJWKS returns a source for the key set at url. A nil client gets a short timeout.
func NewJWKS(url string, client *http.Client, clk clock.Clock) *JWKS {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &JWKS{url: url, client: client, clk: clk}
}

// Key returns the key named by kid, reading the set again when it is stale or lacks the key.
func (j *JWKS) Key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.clk.Now()
	if k, ok := j.keys[kid]; ok && now.Sub(j.fetched) < jwksTTL {
		return k, nil
	}
	if j.keys != nil && now.Sub(j.fetched) < jwksMinGap {
		return nil, fmt.Errorf("no key %q in the key set", kid)
	}
	keys, err := j.fetch(ctx)
	if err != nil {
		if k, ok := j.keys[kid]; ok {
			return k, nil // keep the last set while the issuer cannot be reached
		}
		return nil, err
	}
	j.keys, j.fetched = keys, now
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("no key %q in the key set", kid)
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (j *JWKS) fetch(ctx context.Context) (map[string]crypto.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("key set request: %w", err)
	}
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read the key set: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read the key set: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, jwksMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("read the key set: %w", err)
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err = json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("the key set is not JSON: %w", err)
	}
	out := make(map[string]crypto.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if pub, convErr := k.public(); convErr == nil {
			out[k.Kid] = pub
		}
	}
	return out, nil
}

func b64int(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64url: %w", err)
	}
	return new(big.Int).SetBytes(b), nil
}

// public converts a JSON Web Key to a public key.
func (k jwk) public() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, errN := b64int(k.N)
		e, errE := b64int(k.E)
		if errN != nil || errE != nil || !e.IsInt64() {
			return nil, errors.New("bad RSA key")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		x, errX := base64.RawURLEncoding.DecodeString(k.X)
		y, errY := base64.RawURLEncoding.DecodeString(k.Y)
		if errX != nil || errY != nil || k.Crv != "P-256" || len(x) != 32 || len(y) != 32 {
			return nil, errors.New("bad EC key")
		}
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append([]byte{4}, append(x, y...)...))
		if err != nil {
			return nil, fmt.Errorf("the point is not on the curve: %w", err)
		}
		return pub, nil
	case "OKP":
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || k.Crv != "Ed25519" || len(x) != ed25519.PublicKeySize {
			return nil, errors.New("bad OKP key")
		}
		return ed25519.PublicKey(x), nil
	}
	return nil, errors.New("unsupported key type")
}

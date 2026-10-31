// Package authtest makes tokens and Tool app keys for tests: an issuer with a key pair of its own, and keys in a store.
// It is for tests only; nothing in the engine imports it.
package authtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Issuer signs tokens the way the management app does.
type Issuer struct {
	Name     string
	URL      string
	Audience string
	Kid      string
	Key      *ecdsa.PrivateKey
	Clock    clock.Clock
}

// NewIssuer returns an issuer with a fresh key.
func NewIssuer(name, issuerURL, audience string, clk clock.Clock) *Issuer {
	key := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	return &Issuer{Name: name, URL: issuerURL, Audience: audience, Kid: "k1", Key: key, Clock: clk}
}

// PublicPEM is the issuer's public key in PEM, to pin in the engine's configuration.
func (i *Issuer) PublicPEM() string {
	der := must(x509.MarshalPKIXPublicKey(&i.Key.PublicKey))
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// Claims are the claims of a token; a nil or missing entry takes the default.
type Claims map[string]any

// Token signs a token. The defaults are a token of 60 seconds for the issuer and audience; a claim set to nil is
// left out, and one set to a value replaces the default.
func (i *Issuer) Token(c Claims) string {
	now := i.Clock.Now()
	claims := Claims{
		"iss": i.URL, "aud": i.Audience, "iat": now.Unix(), "exp": now.Add(60 * time.Second).Unix(),
		"jti": randomID(), "scope": "",
	}
	for k, v := range c {
		if v == nil {
			delete(claims, k)
			continue
		}
		claims[k] = v
	}
	return i.sign(map[string]any{"alg": "ES256", "kid": i.Kid, "typ": "JWT"}, claims)
}

// SignClaims signs exactly these claims and adds none, for a test that builds a whole token itself.
func (i *Issuer) SignClaims(c Claims) string {
	return i.sign(map[string]any{"alg": "ES256", "kid": i.Kid, "typ": "JWT"}, c)
}

// TokenFor signs a token for a person in an org with an app role.
func (i *Issuer) TokenFor(sub, org, appRole string, extra Claims) string {
	c := Claims{"sub": sub, "org_id": org, "app_role": appRole}
	for k, v := range extra {
		c[k] = v
	}
	return i.Token(c)
}

// SignWith signs a token with another key, to test a signature that does not verify.
func (i *Issuer) SignWith(key *ecdsa.PrivateKey, c Claims) string {
	other := *i
	other.Key = key
	return other.Token(c)
}

func (i *Issuer) sign(header map[string]any, claims Claims) string {
	enc := base64.RawURLEncoding
	h := must(json.Marshal(header))
	b := must(json.Marshal(claims))
	signed := enc.EncodeToString(h) + "." + enc.EncodeToString(b)
	digest := sha256.Sum256([]byte(signed))
	r, s := must2(ecdsa.Sign(rand.Reader, i.Key, digest[:]))
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signed + "." + enc.EncodeToString(sig)
}

// Unsigned makes a token whose header says alg none.
func Unsigned(claims Claims) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"}) //nolint:errcheck // a map of strings encodes
	b, _ := json.Marshal(claims)                                      //nolint:errcheck // the callers give plain values
	return enc.EncodeToString(h) + "." + enc.EncodeToString(b) + "."
}

func randomID() string {
	b := make([]byte, 12)
	must(rand.Read(b))
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomBase32(n int) string {
	b := make([]byte, n)
	must(rand.Read(b))
	return strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))[:n]
}

// NewKey makes a Tool app key for an org and adds it to the store, and returns the key to present.
func NewKey(store *auth.MemoryKeys, org tenant.ID, label string, scopes ...string) string {
	key, _ := NewKeyWith(store, org, label, time.Time{}, scopes...)
	return key
}

// NewKeyWith is NewKey with an expiry, and also returns the key's hash.
func NewKeyWith(store *auth.MemoryKeys, org tenant.ID, label string, expires time.Time, scopes ...string) (string, [sha256.Size]byte) {
	key := "smt_" + randomBase32(16) + "_" + randomBase32(52)
	hash := auth.HashKey(key)
	store.Add(auth.APIKey{Hash: hash, Org: org, Label: label, Scopes: scopes, Expires: expires})
	return key, hash
}

// RandomKey makes a key in the right shape that no store knows.
func RandomKey() string { return "smt_" + randomBase32(16) + "_" + randomBase32(52) }

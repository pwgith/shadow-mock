package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Token is one admin token: who it stands for, what it may do, and the SHA-256 of its secret.
// The secret itself is not kept, so it cannot leak from a memory dump of the process or a log of
// the configuration.
type Token struct {
	Name string
	Role Role
	Hash [sha256.Size]byte
}

// HashSecret returns the SHA-256 a Token holds for a secret.
func HashSecret(secret string) [sha256.Size]byte { return sha256.Sum256([]byte(secret)) }

// TokenAuthenticator authenticates admin callers by a bearer token. It is the central server's
// authenticator: mock traffic is never authenticated, only the admin API and the metrics.
type TokenAuthenticator struct {
	tokens []Token
}

var _ Authenticator = (*TokenAuthenticator)(nil)

// NewTokenAuthenticator returns an authenticator for the given tokens.
func NewTokenAuthenticator(tokens []Token) *TokenAuthenticator {
	return &TokenAuthenticator{tokens: append([]Token(nil), tokens...)}
}

// Authenticate reads "Authorization: Bearer <token>". Every stored token is compared on every call,
// in constant time, so how long a call takes does not say which token was close.
func (a *TokenAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	secret, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidToken,
			"The admin API needs a token: send it as 'Authorization: Bearer <token>'.")
	}
	sum := HashSecret(secret)
	var found *Token
	for i := range a.tokens {
		if subtle.ConstantTimeCompare(sum[:], a.tokens[i].Hash[:]) == 1 {
			found = &a.tokens[i]
		}
	}
	if found == nil {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidToken, "The token is not recognised.")
	}
	return Principal{UserID: found.Name, OrgID: tenant.Local, Roles: []Role{found.Role}}, nil
}

// bearer returns the token in an Authorization header with the Bearer scheme.
func bearer(h string) (string, bool) {
	scheme, secret, found := strings.Cut(strings.TrimSpace(h), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	secret = strings.TrimSpace(secret)
	return secret, secret != ""
}

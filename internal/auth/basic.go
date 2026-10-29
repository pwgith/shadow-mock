package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// BasicAuthenticator authenticates admin callers with one username and password, sent as HTTP
// Basic authentication (WireMock's --admin-api-basic-auth). Whoever holds them is the admin.
type BasicAuthenticator struct {
	user, pass [sha256.Size]byte
}

var _ Authenticator = (*BasicAuthenticator)(nil)

// NewBasicAuthenticator returns an authenticator for one username and password. Only their hashes
// are kept.
func NewBasicAuthenticator(username, password string) *BasicAuthenticator {
	return &BasicAuthenticator{user: sha256.Sum256([]byte(username)), pass: sha256.Sum256([]byte(password))}
}

// Authenticate reads "Authorization: Basic ...". Both values are always compared, in constant
// time, so how long a call takes does not say which one was wrong.
func (a *BasicAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidToken,
			"The admin API needs a username and password (HTTP Basic authentication).")
	}
	u, p := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(pass))
	okUser := subtle.ConstantTimeCompare(u[:], a.user[:])
	okPass := subtle.ConstantTimeCompare(p[:], a.pass[:])
	if okUser&okPass != 1 {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidToken, "The username or password is not right.")
	}
	return Principal{UserID: user, OrgID: tenant.Local, Roles: []Role{Admin}}, nil
}

// Challenge is the WWW-Authenticate value that goes with a 401.
func (a *BasicAuthenticator) Challenge() string { return `Basic realm="shadowmock"` }

// Challenge returns the WWW-Authenticate value an authenticator asks clients to answer with.
func Challenge(a Authenticator) string {
	if c, ok := a.(interface{ Challenge() string }); ok {
		return c.Challenge()
	}
	return `Bearer realm="shadowmock"`
}

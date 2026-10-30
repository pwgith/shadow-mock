package auth

import (
	"net/http"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

// SystemOnly wraps an authenticator so that only a token with the system scope passes: the management app's
// own calls. It guards what is not an org's, such as the metrics, which can show every org's handle and volume.
type SystemOnly struct{ Inner Authenticator }

var _ Authenticator = SystemOnly{}

// Authenticate lets only a system token through.
func (s SystemOnly) Authenticate(r *http.Request) (Principal, error) {
	p, err := s.Inner.Authenticate(r)
	if err != nil {
		return Principal{}, err
	}
	if p.Kind != KindToken || p.Scope != ScopeSystem {
		return Principal{}, apierr.New(http.StatusForbidden, apierr.Forbidden, "This is for the management app.")
	}
	return p, nil
}

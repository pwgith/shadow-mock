package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

func tokens() *auth.TokenAuthenticator {
	return auth.NewTokenAuthenticator([]auth.Token{
		{Name: "ci", Role: auth.Editor, Hash: auth.HashSecret("ci-secret-0123456789")},
		{Name: "ops", Role: auth.Admin, Hash: auth.HashSecret("ops-secret-0123456789")},
	})
}

func request(t *testing.T, header string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__admin/mappings", http.NoBody)
	if header != "" {
		r.Header.Set("Authorization", header)
	}
	return r
}

func TestAnAdminTokenIdentifiesItsOwner(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, header, user string
		role               auth.Role
	}{
		{"an editor token", "Bearer ci-secret-0123456789", "ci", auth.Editor},
		{"an admin token", "Bearer ops-secret-0123456789", "ops", auth.Admin},
		{"the scheme is case-insensitive", "bearer ops-secret-0123456789", "ops", auth.Admin},
		{"extra spaces are tolerated", "  Bearer   ci-secret-0123456789  ", "ci", auth.Editor},
	} {
		p, err := tokens().Authenticate(request(t, tt.header))
		if err != nil || p.UserID != tt.user || p.OrgID != tenant.Local || !p.Has(tt.role) {
			t.Errorf("%s: principal %+v, err %v", tt.name, p, err)
		}
	}
}

func TestACallWithoutAValidTokenIsUnauthorised(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, header string }{
		{"no header", ""},
		{"a token that is not known", "Bearer nope-nope-nope-nope"},
		{"an empty token", "Bearer "},
		{"the wrong scheme", "Basic ci-secret-0123456789"},
		{"the token without a scheme", "ci-secret-0123456789"},
		{"a prefix of a real token", "Bearer ci-secret-012345678"},
		{"a real token with more on the end", "Bearer ci-secret-0123456789x"},
	} {
		_, err := tokens().Authenticate(request(t, tt.header))
		var ae *apierr.Error
		if err == nil || !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized || ae.Code != apierr.InvalidToken {
			t.Errorf("%s: got %v, want 401 invalid_token", tt.name, err)
		}
	}
}

func TestATokenRoleLimitsWhatItMayDo(t *testing.T) {
	t.Parallel()
	az := auth.RoleAuthorizer{}
	editor, _ := tokens().Authenticate(request(t, "Bearer ci-secret-0123456789"))
	admin, _ := tokens().Authenticate(request(t, "Bearer ops-secret-0123456789"))
	res := auth.Resource{Tenant: tenant.Local, Global: true}
	if err := az.Authorize(t.Context(), editor, auth.SessionWrite, res); err != nil {
		t.Errorf("an editor may write sessions: %v", err)
	}
	if err := az.Authorize(t.Context(), editor, auth.GlobalWrite, res); err == nil {
		t.Error("an editor must not change the global session")
	}
	if err := az.Authorize(t.Context(), admin, auth.GlobalWrite, res); err != nil {
		t.Errorf("an admin may change the global session: %v", err)
	}
}

func TestNoTokensAuthenticatesNobody(t *testing.T) {
	t.Parallel()
	if _, err := auth.NewTokenAuthenticator(nil).Authenticate(request(t, "Bearer anything")); err == nil {
		t.Error("an authenticator with no tokens must refuse every call")
	}
}

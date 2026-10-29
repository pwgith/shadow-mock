package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pwgith/shadow-mock/internal/auth"
)

func TestBasicAuthenticator(t *testing.T) {
	t.Parallel()
	a := auth.NewBasicAuthenticator("ada", "s3cret")
	tests := []struct {
		name, user, pass string
		send, ok         bool
	}{
		{"the right credentials", "ada", "s3cret", true, true},
		{"the wrong password", "ada", "nope", true, false},
		{"the wrong user", "bob", "s3cret", true, false},
		{"nothing sent", "", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__admin/mappings", http.NoBody)
			if tt.send {
				r.SetBasicAuth(tt.user, tt.pass)
			}
			p, err := a.Authenticate(r)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok = %v", err, tt.ok)
			}
			if tt.ok && !p.Has(auth.Admin) {
				t.Errorf("principal %+v should be an admin", p)
			}
		})
	}
	if got := auth.Challenge(a); got != `Basic realm="shadowmock"` {
		t.Errorf("challenge = %q", got)
	}
	if got := auth.Challenge(auth.NoopAuthenticator{}); got != `Bearer realm="shadowmock"` {
		t.Errorf("default challenge = %q", got)
	}
}

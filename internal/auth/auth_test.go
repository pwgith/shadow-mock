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

func TestNoopAuthenticator(t *testing.T) {
	t.Parallel()
	p, err := auth.NoopAuthenticator{}.Authenticate(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != "local" || p.OrgID != tenant.Local || !p.Has(auth.Admin) {
		t.Errorf("Authenticate() = %+v, want the local admin", p)
	}
}

func TestPrincipalHas(t *testing.T) {
	t.Parallel()
	tests := []struct {
		have []auth.Role
		need auth.Role
		want bool
	}{
		{[]auth.Role{auth.Admin}, auth.Viewer, true},
		{[]auth.Role{auth.Admin}, auth.Admin, true},
		{[]auth.Role{auth.Editor}, auth.Admin, false},
		{[]auth.Role{auth.Editor}, auth.Viewer, true},
		{[]auth.Role{auth.Viewer}, auth.Editor, false},
		{[]auth.Role{auth.Viewer, auth.Editor}, auth.Editor, true},
		{[]auth.Role{"unknown"}, auth.Viewer, false},
		{nil, auth.Viewer, false},
	}
	for _, tt := range tests {
		if got := (auth.Principal{Roles: tt.have}).Has(tt.need); got != tt.want {
			t.Errorf("%v.Has(%s) = %v, want %v", tt.have, tt.need, got, tt.want)
		}
	}
}

func TestRoleAuthorizer(t *testing.T) {
	t.Parallel()
	local := tenant.Local
	person := func(org tenant.ID, role auth.Role) auth.Principal {
		return auth.Principal{UserID: "u", OrgID: org, Roles: []auth.Role{role}}
	}
	tests := []struct {
		name     string
		deny     bool
		p        auth.Principal
		action   auth.Action
		resource auth.Resource
		wantCode apierr.Code // empty means allowed
	}{
		{"a viewer reads", false, person(local, auth.Viewer), auth.Read, auth.Resource{Tenant: local}, ""},
		{"a viewer cannot write sessions", false, person(local, auth.Viewer), auth.SessionWrite, auth.Resource{Tenant: local}, apierr.Forbidden},
		{"an editor writes sessions", false, person(local, auth.Editor), auth.SessionWrite, auth.Resource{Tenant: local}, ""},
		{"an editor cannot write globally", false, person(local, auth.Editor), auth.GlobalWrite, auth.Resource{Tenant: local, Global: true}, apierr.Forbidden},
		{"an admin writes globally", false, person(local, auth.Admin), auth.GlobalWrite, auth.Resource{Tenant: local, Global: true}, ""},
		{"an admin can control the system", false, person(local, auth.Admin), auth.SystemControl, auth.Resource{Tenant: local}, ""},
		{"another org is refused", false, person("globex", auth.Admin), auth.Read, auth.Resource{Tenant: local}, apierr.OrgMismatch},
		{"global writes can be reserved for the management app", true, person(local, auth.Admin), auth.GlobalWrite, auth.Resource{Tenant: local, Global: true}, apierr.GlobalWritesViaManagementApp},
		{"system control is reserved too", true, person(local, auth.Admin), auth.SystemControl, auth.Resource{Tenant: local}, apierr.GlobalWritesViaManagementApp},
		{"session writes stay open when global writes are reserved", true, person(local, auth.Editor), auth.SessionWrite, auth.Resource{Tenant: local}, ""},
		{"reads stay open when global writes are reserved", true, person(local, auth.Viewer), auth.Read, auth.Resource{Tenant: local, Global: true}, ""},
		{"an unknown action needs the admin role", false, person(local, auth.Editor), "other", auth.Resource{Tenant: local}, apierr.Forbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := auth.RoleAuthorizer{DenyGlobalWrites: tt.deny}.Authorize(t.Context(), tt.p, tt.action, tt.resource)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("Authorize() = %v, want allowed", err)
				}
				return
			}
			var ae *apierr.Error
			if !errors.As(err, &ae) || ae.Code != tt.wantCode || ae.Status != http.StatusForbidden {
				t.Fatalf("Authorize() = %v, want a 403 %s", err, tt.wantCode)
			}
		})
	}
}

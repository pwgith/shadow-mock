// Package auth authenticates admin callers and authorises their actions (DESIGN.md §4.1).
// Standalone uses NoopAuthenticator, but the same chain runs, so a missing check shows up in
// tests rather than in production.
package auth

import (
	"context"
	"net/http"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Role is an org-level role.
type Role string

// Roles, from least to most privileged.
const (
	Viewer Role = "viewer"
	Editor Role = "editor"
	Admin  Role = "admin"
)

// Principal is the authenticated caller.
type Principal struct {
	UserID string
	OrgID  tenant.ID
	Roles  []Role
}

// Has reports whether the principal holds role r or a more privileged one.
func (p Principal) Has(r Role) bool {
	for _, have := range p.Roles {
		if rank(have) >= rank(r) {
			return true
		}
	}
	return false
}

func rank(r Role) int {
	switch r {
	case Viewer:
		return 1
	case Editor:
		return 2
	case Admin:
		return 3
	default:
		return 0
	}
}

// Authenticator identifies the caller of an admin request.
type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
}

// NoopAuthenticator is standalone mode's authenticator: every caller is the local admin (FR-9.2).
type NoopAuthenticator struct{}

var _ Authenticator = NoopAuthenticator{}

// Authenticate returns the local admin principal.
func (NoopAuthenticator) Authenticate(*http.Request) (Principal, error) {
	return Principal{UserID: "local", OrgID: tenant.Local, Roles: []Role{Admin}}, nil
}

// Action is something a principal may do.
type Action string

// Actions checked by the admin API.
const (
	Read          Action = "read"
	SessionWrite  Action = "sessions:write"
	GlobalWrite   Action = "global:write"
	SystemControl Action = "system:control"
)

// Resource is what an action applies to.
type Resource struct {
	Tenant tenant.ID
	// Global is true for the org's global session, false for a key-based or URL-based one.
	Global bool
}

// Authorizer decides whether a principal may act on a resource.
type Authorizer interface {
	Authorize(ctx context.Context, p Principal, a Action, res Resource) error
}

// RoleAuthorizer checks the org and role (FR-9.4), and optionally rejects global writes
// because the management app makes them (FR-9.5a).
type RoleAuthorizer struct {
	// DenyGlobalWrites rejects global-scope writes (admin.globalWrites: deny).
	DenyGlobalWrites bool
}

var _ Authorizer = RoleAuthorizer{}

// Authorize implements Authorizer.
func (a RoleAuthorizer) Authorize(_ context.Context, p Principal, act Action, res Resource) error {
	if p.OrgID != res.Tenant {
		return apierr.New(http.StatusForbidden, apierr.OrgMismatch,
			"Token is for org %s, URL is for org %s; switch active org.", p.OrgID, res.Tenant)
	}
	need := required(act)
	if !p.Has(need) {
		return apierr.New(http.StatusForbidden, apierr.Forbidden, "This action needs the %s role.", need)
	}
	if (act == GlobalWrite || act == SystemControl) && a.DenyGlobalWrites {
		return apierr.New(http.StatusForbidden, apierr.GlobalWritesViaManagementApp,
			"Global session changes are made in the management app.")
	}
	return nil
}

func required(a Action) Role {
	switch a {
	case Read:
		return Viewer
	case SessionWrite:
		return Editor
	case GlobalWrite, SystemControl:
		return Admin
	default:
		return Admin
	}
}

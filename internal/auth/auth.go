// Package auth authenticates admin callers and authorises their actions (DESIGN.md §4.1).
// Standalone uses NoopAuthenticator, but the same chain runs, so a missing check shows up in
// tests rather than in production.
package auth

import (
	"context"
	"net/http"
	"slices"

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
	// Managed mode only: where the principal came from and what it may reach.
	Kind      Kind
	Scope     string   // metadata, system or ""
	Issuer    string   // the trusted issuer that signed a token
	TokenID   string   // the token's jti
	KeyLabel  string   // the label of a Tool app key
	KeyScopes []string // the scopes of a Tool app key
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
	// Validate checks a mapping or dry-runs a request without changing anything (the `validate` scope).
	Validate Action = "validate"
	// Manage is what only the management app does for itself: purge an org, anonymise a person.
	Manage Action = "manage"
)

// Resource is what an action applies to.
type Resource struct {
	Tenant tenant.ID
	// Global is true for the org's global session, false for a key-based or URL-based one.
	Global bool
	// Lifecycle is true for creating, listing or renewing a shadow mock, which a global path serves
	// but which is not a write to the global layer.
	Lifecycle bool
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
	if p.Kind == KindKey {
		return authorizeKey(p, act, res)
	}
	if act == Manage && (p.Scope != ScopeSystem || !p.Has(Admin)) {
		return apierr.New(http.StatusForbidden, apierr.Forbidden, "Only the management app can do this.")
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

// authorizeKey decides what a Tool app key may do (FR-11.3). A key never writes the global layer,
// the settings or the global journal, and never manages an org.
func authorizeKey(p Principal, act Action, res Resource) error {
	has := func(scope string) bool { return slices.Contains(p.KeyScopes, scope) }
	allowed := false
	switch act {
	case Read:
		allowed = has(ScopeRead) || has(ScopeSessions)
	case Validate:
		allowed = has(ScopeValidate)
	case SessionWrite:
		allowed = has(ScopeSessions) && (!res.Global || res.Lifecycle)
	case GlobalWrite, SystemControl, Manage:
		return apierr.New(http.StatusForbidden, apierr.GlobalWritesViaManagementApp,
			"Global session changes are made in the management app.")
	}
	if !allowed {
		return apierr.New(http.StatusForbidden, apierr.Forbidden, "This key's scopes do not allow that.")
	}
	return nil
}

func required(a Action) Role {
	switch a {
	case Read, Validate:
		return Viewer
	case SessionWrite:
		return Editor
	case GlobalWrite, SystemControl:
		return Admin
	default:
		return Admin
	}
}

package auth

import (
	"net/http"
	"strings"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// authenticateToken checks a token from a trusted issuer, in the order of FR-11.2: the issuer it names,
// its signature, the audience, the dates and lifetime, the org, then replay.
func (m *Managed) authenticateToken(r *http.Request, cred string) (Principal, error) {
	t, err := parseJWT(cred)
	if err != nil {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidToken, "The token is not a valid JWT.")
	}
	iss, ok := m.issuer(t.claims.Issuer)
	if !ok {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.UntrustedIssuer, "The token's issuer is not trusted here.")
	}
	key, err := iss.Keys.Key(r.Context(), t.header.Kid)
	if err != nil {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidToken, "The token's signature cannot be checked.")
	}
	if err = t.verify(key); err != nil {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidToken, "The token's signature is not valid.")
	}
	if !contains(t.claims.Audience, iss.Audience) {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.InvalidAudience, "The token is for another audience.")
	}
	if dateErr := m.checkDates(t, iss); dateErr != nil {
		return Principal{}, dateErr
	}
	p, err := principalOf(t, iss)
	if err != nil {
		return Principal{}, err
	}
	if iss.ReplayCheck && m.replay != nil && t.claims.ID != "" && m.replay.Seen(iss.Name, t.claims.ID, t.claims.Expires.Time) {
		return Principal{}, apierr.New(http.StatusUnauthorized, apierr.TokenReplayed, "The token has been used already.")
	}
	return p, nil
}

func (m *Managed) issuer(name string) (Issuer, bool) {
	for _, i := range m.issuers {
		if i.Issuer == name {
			return i, true
		}
	}
	return Issuer{}, false
}

func contains(list audience, want string) bool {
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}

// checkDates enforces exp, nbf and the engine's own cap on the lifetime: whatever the issuer says, a
// token that lives longer than the cap is refused.
func (m *Managed) checkDates(t *jwt, iss Issuer) error {
	now := m.clk.Now()
	exp := t.claims.Expires
	if exp == nil || !exp.After(now.Add(-clockSkew)) {
		return apierr.New(http.StatusUnauthorized, apierr.TokenExpired, "The token has expired.")
	}
	if nbf := t.claims.NotAfter; nbf != nil && nbf.After(now.Add(clockSkew)) {
		return apierr.New(http.StatusUnauthorized, apierr.TokenNotActive, "The token is not valid yet.")
	}
	issued := now
	if iat := t.claims.IssuedAt; iat != nil {
		issued = iat.Time
	}
	if exp.Sub(issued) > iss.maxLifetime()+clockSkew || exp.Sub(now) > iss.maxLifetime()+clockSkew {
		return apierr.New(http.StatusUnauthorized, apierr.TokenTooLong, "The token lives longer than %s.", iss.maxLifetime())
	}
	return nil
}

// principalOf reads who the token is for.
func principalOf(t *jwt, iss Issuer) (Principal, error) {
	if t.claims.OrgID == "" {
		return Principal{}, apierr.New(http.StatusForbidden, apierr.NoActiveOrg, "The token has no active org: pick an org and try again.")
	}
	role, ok := iss.role(t.claims.AppRole)
	if !ok {
		return Principal{}, apierr.New(http.StatusForbidden, apierr.Forbidden, "The token has no role this server knows.")
	}
	scope := ""
	for _, s := range strings.Fields(t.claims.Scope) {
		switch s {
		case ScopeMetadata:
			scope = ScopeMetadata // the narrowest scope wins
		case ScopeSystem:
			if scope == "" {
				scope = ScopeSystem
			}
		}
	}
	return Principal{
		UserID: t.claims.Subject, OrgID: tenant.ID(t.claims.OrgID), Roles: []Role{role},
		Kind: KindToken, Issuer: iss.Name, TokenID: t.claims.ID, Scope: scope,
	}, nil
}

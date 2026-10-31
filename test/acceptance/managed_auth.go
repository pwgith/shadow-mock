package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/auth/authtest"
)

// appRoleOf turns the role names the specs use into the app roles the management app signs (FR-11.2).
func appRoleOf(role string) string {
	switch role {
	case "editor":
		return "member"
	default:
		return role
	}
}

// claimChange applies one row of a "token with:" table to a spec.
func claimChange(sp *tokenSpec, claim, value string) error {
	now := time.Now()
	switch claim {
	case "signature":
		other := authtest.NewIssuer("someone-else", managedIssuerURL, managedAudience, nil)
		sp.sign = func(_ *authtest.Issuer, c authtest.Claims) string { return other.SignClaims(c) }
	case "alg":
		switch {
		case value == "none":
			sp.sign = func(_ *authtest.Issuer, c authtest.Claims) string { return authtest.Unsigned(c) }
		case strings.HasPrefix(value, "HS256"):
			sp.sign = func(i *authtest.Issuer, c authtest.Claims) string { return hs256([]byte(i.PublicPEM()), c) }
		default:
			return fmt.Errorf("claim alg: do not know %q", value)
		}
	case "iss":
		sp.set["iss"] = value
	case "aud":
		sp.set["aud"] = "mocks-staging"
	case "exp":
		if value != "1 minute ago" {
			return fmt.Errorf("claim exp: do not know %q", value)
		}
		sp.set["exp"] = now.Add(-time.Minute).Unix()
		sp.set["iat"] = now.Add(-2 * time.Minute).Unix()
	case "lifetime":
		d, err := time.ParseDuration(strings.ReplaceAll(strings.ReplaceAll(value, " minutes", "m"), " hours", "h"))
		if err != nil {
			return fmt.Errorf("claim lifetime: %w", err)
		}
		sp.set["exp"] = now.Add(d).Unix()
	case "org_id":
		sp.org = strings.Trim(strings.TrimPrefix(value, "the org "), `"`)
	default:
		return fmt.Errorf("do not know the claim %q", claim)
	}
	return nil
}

func (w *world) tokenOf(sub, org, role string) tokenSpec {
	return tokenSpec{sub: sub, org: org, role: role, set: authtest.Claims{}}
}

func (w *world) initManagedAuth(sc *godog.ScenarioContext) {
	managed := func(name string) error { return w.startManaged(name) }
	sc.Step(`^Shadow Mock is running in managed mode with the trusted issuer "([^"]*)"$`, managed)
	sc.Step(`^Shadow Mock is running in SaaS mode$`, func() error { return managed("management-app") })
	sc.Step(`^the org "([^"]*)" with handle "([^"]*)" on plan "([^"]*)"$`, func(n, h, p string) error { return w.addOrg(n, h, p, 0, 0) })
	sc.Step(`^the org "([^"]*)" with handle "([^"]*)" on plan "([^"]*)" limited to (\d+) requests per second with a burst of (\d+)$`,
		func(n, h, p string, rps, burst int) error { return w.addOrg(n, h, p, float64(rps), burst) })
	sc.Step(`^a Tool app key "([^"]*)" for org "([^"]*)" with scopes "([^"]*)"$`, func(label, org, scopes string) error {
		return w.addKey(label, org, strings.Split(scopes, ","))
	})

	// How the engine trusts the issuer.
	sc.Step(`^Shadow Mock expects the audience "([^"]*)"$`, func(a string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		m.audience = a
		m.signer.Audience = a
		return w.reconfigureManaged()
	})
	sc.Step(`^Shadow Mock is configured with the issuer's public key pinned$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		m.pinned = true
		return w.reconfigureManaged()
	})
	sc.Step(`^replay checking is on$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		m.replay = true
		return w.reconfigureManaged()
	})
	sc.Step(`^the issuer publishes a new key and signs tokens with it$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if ensureErr := w.ensure(); ensureErr != nil {
			return ensureErr
		}
		// Make the engine read the key set once, so that the new key is a rotation and not the first read.
		if _, err = w.managedAdminCall("GET", "/sessions", "", m.defaultOrg, &credential{bearer: func() (string, error) {
			return m.mint(w, w.tokenOf("alice", m.defaultOrg, "member")), nil
		}}, nil); err != nil {
			return err
		}
		next := authtest.NewIssuer(m.name, managedIssuerURL, m.audience, w.clk)
		next.Kid = "k2"
		m.jwksMu.Lock()
		m.published = append(m.published, next)
		m.jwksMu.Unlock()
		m.signer = next
		w.clk.advance(20 * time.Second) // past the least time between key set reads
		return nil
	})
	sc.Step(`^the issuer signs a token that expires in 1 hour$`, func() error {
		sp := w.tokenOf("alice", "acme", "member")
		sp.set["exp"] = time.Now().Add(time.Hour).Unix()
		return w.presentToken(sp)
	})

	// Credentials.
	token := func(sub, org, role string) error { return w.presentToken(w.tokenOf(sub, org, appRoleOf(role))) }
	sc.Step(`^I present a token for "([^"]*)" in org "([^"]*)" with app role "([^"]*)"$`, token)
	sc.Step(`^I am "([^"]*)" in org "([^"]*)" with app role "([^"]*)"$`, token)
	sc.Step(`^I am "([^"]*)" in org "([^"]*)" with role "([^"]*)"$`, token)
	sc.Step(`^I am "([^"]*)" with active org "([^"]*)"$`, func(sub, org string) error { return token(sub, org, "member") })
	sc.Step(`^"([^"]*)" is a member of orgs "([^"]*)" and "([^"]*)"$`, func(string, string, string) error { return nil })
	sc.Step(`^"([^"]*)" is in no teams$`, func(string) error { return nil })
	sc.Step(`^I present a token for "([^"]*)" in org "([^"]*)" with app role "([^"]*)" and scope "([^"]*)"$`, func(sub, org, role, scope string) error {
		sp := w.tokenOf(sub, org, role)
		sp.scope = scope
		return w.presentToken(sp)
	})
	sc.Step(`^I present a token with audience "([^"]*)"$`, func(aud string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf("alice", m.defaultOrg, "member")
		sp.set["aud"] = aud
		return w.presentToken(sp)
	})
	sc.Step(`^I present a token with no "([^"]*)" claim$`, func(claim string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf("alice", m.defaultOrg, "member")
		sp.set[claim] = nil
		return w.presentToken(sp)
	})
	sc.Step(`^I present a token with:$`, func(t *godog.Table) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf("alice", m.defaultOrg, "member")
		for _, row := range t.Rows[1:] {
			if claimErr := claimChange(&sp, w.sub(row.Cells[0].Value), w.sub(row.Cells[1].Value)); claimErr != nil {
				return claimErr
			}
		}
		return w.presentToken(sp)
	})
	sc.Step(`^I am the Tool app key "([^"]*)" in org "([^"]*)"$`, func(label, org string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if _, ok := m.keys[label]; !ok {
			if keyErr := w.addKey(label, org, []string{"sessions", "read"}); keyErr != nil {
				return keyErr
			}
		}
		c := m.keyCred(label)
		c.org = org
		m.cred = c
		return nil
	})

	// Calls.
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with body "([^"]*)"$`, func(me, p, b string) error { return w.callWith(me, p, b, nil) })
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with the key "([^"]*)"$`, func(me, p, k string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.callWith(me, p, "", m.keyCred(k))
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with the key "([^"]*)" and body "([^"]*)"$`, func(me, p, k, b string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.callWith(me, p, b, m.keyCred(k))
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" on org "([^"]*)" with the key "([^"]*)"$`, func(me, p, org, k string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		_, err = w.managedAdminCall(me, p, "", org, m.keyCred(k), nil)
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with the key "([^"]*)" from "([^"]*)"$`, func(me, p, k, ip string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		_, err = w.managedAdminCall(me, p, "", "", m.keyCred(k), http.Header{"X-Forwarded-For": {ip}})
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with a token for "([^"]*)" in org "([^"]*)"$`, func(me, p, sub, org string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf(sub, org, "member")
		_, err = w.managedAdminCall(me, p, "", org, &credential{org: org, bearer: func() (string, error) { return m.mint(w, sp), nil }}, nil)
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with a token signed by the new key$`, func(me, p string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf("alice", m.defaultOrg, "member")
		return w.callWith(me, p, "", &credential{bearer: func() (string, error) { return m.mint(w, sp), nil }})
	})
	sc.Step(`^the SUT sends "([A-Z]+) ([^"]*)"$`, func(method, path string) error {
		_, err := w.send(newRequest(method, path))
		return err
	})
	sc.Step(`^the SUT sends "([A-Z]+) ([^"]*)" with the header "([^:"]*): ([^"]*)"$`, func(method, path, name, value string) error {
		r := newRequest(method, path)
		r.header.Set(name, value)
		_, err := w.send(r)
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with the same token$`, func(me, p string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		prev := m.lastBearer
		_, err = w.managedAdminCall(me, p, "", m.defaultOrg, &credential{bearer: func() (string, error) { return prev, nil }}, nil)
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" as a member$`, func(me, p string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf("alice", m.defaultOrg, "member")
		return w.callWith(me, p, "", &credential{org: m.defaultOrg, bearer: func() (string, error) { return m.mint(w, sp), nil }})
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" using the token as a key$`, func(me, p string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		tok := m.mint(w, w.tokenOf("alice", m.defaultOrg, "member"))
		return w.callWith(me, p, "", &credential{bearer: func() (string, error) { return tok, nil }})
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with a key that has the wrong secret$`, func(me, p string) error {
		return w.callWith(me, p, "", &credential{bearer: func() (string, error) { return authtest.RandomKey(), nil }})
	})

	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with the credential "([^"]*)"$`, func(me, p, c string) error {
		return w.callWith(me, p, "", bearerCred(c))
	})

	// Assertions.
	sc.Step(`^the response contains no mappings, sessions or journal entries$`, func() error {
		if w.last == nil {
			return fmt.Errorf("no response yet")
		}
		var doc map[string]any
		if json.Unmarshal(w.last.body, &doc) != nil {
			return nil //nolint:nilerr // not a document with lists, so it has none
		}
		for _, k := range []string{"mappings", "sessions", "requests"} {
			if _, ok := doc[k]; ok {
				return fmt.Errorf("the response has %q: %s", k, truncate(w.last.body))
			}
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is not "([^"]*)"$`, func(path, not string) error {
		got, found, err := w.jsonOne(path)
		if err != nil {
			return nil //nolint:nilerr // a body that is not a JSON document has nothing at the path, so it is not that value
		}
		if found && text(got) == not {
			return fmt.Errorf("%s is %q, want anything else", path, got)
		}
		return nil
	})
	sc.Step(`^the audit trail has an entry with issuer "([^"]*)", subject "([^"]*)" and the token's id$`, func(iss, sub string) error {
		for _, e := range w.app.Audit().List(ctxBackground(), "acme") {
			if e.Issuer == iss && e.Subject == sub && e.TokenID != "" {
				return nil
			}
		}
		return fmt.Errorf("no audit entry for %s/%s with a token id", iss, sub)
	})
	_ = auth.Challenge
}

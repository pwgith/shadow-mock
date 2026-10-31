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
	"github.com/pwgith/shadow-mock/internal/server"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// bearerCred is a credential that presents a fixed string.
func bearerCred(v string) *credential {
	return &credential{bearer: func() (string, error) { return v, nil }}
}

func (w *world) initManagedKeys(sc *godog.ScenarioContext) {
	var (
		testKey       string // the key a "the key is …" step set up
		lookupsBefore int64
		usedLabel     string
	)
	secretOf := func(label string) (string, error) {
		m, err := w.mgd()
		if err != nil {
			return "", err
		}
		k, ok := m.keys[label]
		if !ok {
			return "", fmt.Errorf("no Tool app key %q has been created", label)
		}
		return k, nil
	}
	sc.Step(`^the key is (made up|the right id and a wrong secret|expired|revoked longer than a minute ago)$`, func(state string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		real, err := secretOf("ci-tests")
		if err != nil {
			return err
		}
		switch state {
		case "made up":
			testKey = authtest.RandomKey()
		case "the right id and a wrong secret":
			id := strings.SplitN(real, "_", 3)[1]
			testKey = "smt_" + id + "_" + strings.SplitN(authtest.RandomKey(), "_", 3)[2]
		case "expired":
			testKey, _ = authtest.NewKeyWith(w.app.Keys(), tenant.ID(m.defaultOrg), "expired", w.clk.Now().Add(-time.Hour), "sessions", "read")
		default: // revoked longer than a minute ago
			w.app.Keys().Revoke(auth.HashKey(real))
			w.clk.advance(2 * time.Minute)
			testKey = real
		}
		return nil
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with the key$`, func(method, path string) error {
		return w.callWith(method, path, "", bearerCred(testKey))
	})
	sc.Step(`^a mock set key "([^"]*)"$`, func(k string) error {
		testKey = k
		lookupsBefore = w.app.Keys().Lookups()
		return nil
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with that key$`, func(method, path string) error {
		return w.callWith(method, path, "", bearerCred(testKey))
	})
	sc.Step(`^the key is not looked up in the database$`, func() error {
		if n := w.app.Keys().Lookups(); n != lookupsBefore {
			return fmt.Errorf("the key store was asked %d times for a key that must be refused on sight", n-lookupsBefore)
		}
		return nil
	})
	sc.Step(`^the key "([^"]*)" has been used$`, func(label string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		usedLabel = label
		rep, err := w.managedAdminCall("GET", "/sessions", "", "", m.keyCred(label), nil)
		if err != nil {
			return err
		}
		if rep.status != http.StatusOK {
			return fmt.Errorf("using the key gave %d: %s", rep.status, truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^the management app revokes the key$`, func() error {
		k, err := secretOf(usedLabel)
		if err != nil {
			return err
		}
		w.app.Keys().Revoke(auth.HashKey(k))
		return nil
	})
	sc.Step(`^(\d+) seconds pass$`, func(n int) error { w.clk.advance(time.Duration(n) * time.Second); return nil })
	sc.Step(`^one minute passes$`, func() error { w.clk.advance(time.Minute); return nil })
	sc.Step(`^the response is the same as for a key that does not exist$`, func() error {
		first := w.last
		unknown, err := w.managedAdminCall("GET", "/sessions", "", "", bearerCred(authtest.RandomKey()), nil)
		if err != nil {
			return err
		}
		if first.status != unknown.status || string(first.body) != string(unknown.body) {
			return fmt.Errorf("a wrong secret is told apart from an unknown key: %d %s against %d %s", first.status, truncate(first.body), unknown.status, truncate(unknown.body))
		}
		return nil
	})
	sc.Step(`^no log line contains the secret$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if m.lastBearer != "" && strings.Contains(w.logs.String(), m.lastBearer) {
			return fmt.Errorf("a log line contains the credential that was presented")
		}
		return nil
	})
	sc.Step(`^the key's last used time and address "([^"]*)" are written to the key-usage table$`, func(ip string) error {
		k, err := secretOf("ci-tests")
		if err != nil {
			return err
		}
		use, ok := w.app.Keys().LastUse(auth.HashKey(k))
		if !ok || use.IP != ip || use.At.IsZero() {
			return fmt.Errorf("last use = %+v (recorded: %v), want the address %s", use, ok, ip)
		}
		return nil
	})
	sc.Step(`^no configuration reload happens$`, func() error {
		if w.logContains("rebuild") {
			return fmt.Errorf("the engine rebuilt a layer: %s", w.logs.String())
		}
		return nil
	})
	sc.Step(`^the org's IP allowlist is "([^"]*)" and applies to the admin API$`, func(cidr string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.setAllowlist(m.defaultOrg, []string{cidr}, true)
	})
	sc.Step(`^a call with a token from the trusted issuer from "([^"]*)" is allowed$`, func(ip string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf("alice", m.defaultOrg, "member")
		rep, err := w.managedAdminCall("GET", "/sessions", "", "", &credential{bearer: func() (string, error) { return m.mint(w, sp), nil }}, http.Header{"X-Forwarded-For": {ip}})
		if err != nil {
			return err
		}
		if rep.status != http.StatusOK {
			return fmt.Errorf("a token from the trusted issuer from %s gave %d: %s", ip, rep.status, truncate(rep.body))
		}
		return nil
	})

	sc.Step(`^within (\d+) s a "([A-Z]+)" request to "([^"]*)" returns body "([^"]*)"$`, func(secs int, method, path, want string) error {
		return w.eventually(time.Duration(secs)*time.Second, func() error {
			rep, err := w.send(newRequest(method, path))
			if err != nil {
				return err
			}
			if string(rep.body) != want {
				return fmt.Errorf("%s %s gave %d %q, want the body %q", method, path, rep.status, truncate(rep.body), want)
			}
			return nil
		})
	})

	// The metadata scope.
	sc.Step(`^a key-based shadow mock "([^"]*)" that served (\d+) requests, one with a request body "([^"]*)"$`, func(name string, n int, body string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.record(func() error {
			rep, callErr := w.managedAdminCall("POST", "/sessions/key-based", `{"name":"`+name+`"}`, "", w.setupCred(m.defaultOrg), nil)
			if callErr != nil {
				return callErr
			}
			if err := w.noteSession(name, rep); err != nil {
				return err
			}
			for i := range n {
				r := newRequest("GET", "/"+m.orgs[m.defaultOrg].handle+"/something")
				if i == 0 {
					r.method, r.body = "POST", []byte(body)
				}
				r.header.Set("X-Shadow-Mock-Key", w.vars[name+".key"])
				if _, sendErr := w.send(r); sendErr != nil {
					return sendErr
				}
			}
			return nil
		})
	})
	sc.Step(`^each session shows its kind, name, creator, expiry and request count$`, func() error {
		return w.eachItemHas("$.sessions", "kind", "name", "createdBy", "expiresAt", "requestCount")
	})
	sc.Step(`^each entry shows its method, path, status, layer and time$`, func() error {
		if err := w.eachItemHas("$.requests", "request", "response", "shadowMock"); err != nil {
			return err
		}
		return w.eachEntry(func(e map[string]any) error {
			req, _ := e["request"].(map[string]any)       //nolint:errcheck // checked below
			resp, _ := e["response"].(map[string]any)     //nolint:errcheck // checked below
			shadow, _ := e["shadowMock"].(map[string]any) //nolint:errcheck // checked below
			if req["method"] == nil || req["url"] == nil || resp["status"] == nil || shadow["layer"] == nil || req["loggedDate"] == nil {
				return fmt.Errorf("an entry lacks its method, path, status, layer or time: %v", e)
			}
			return nil
		})
	})
	sc.Step(`^no entry has a request body, a response body or headers' values$`, func() error {
		return w.eachEntry(func(e map[string]any) error {
			for _, part := range []string{"request", "response"} {
				p, _ := e[part].(map[string]any)                                                                //nolint:errcheck // a missing part has nothing to check
				if b, _ := p["body"].(string); b != "" || p["bodyAsBase64"] != nil && p["bodyAsBase64"] != "" { //nolint:errcheck // see above
					return fmt.Errorf("the %s of an entry has a body: %v", part, p)
				}
				if h, _ := p["headers"].(map[string]any); len(h) != 0 { //nolint:errcheck // see above
					return fmt.Errorf("the %s of an entry has headers: %v", part, h)
				}
			}
			return nil
		})
	})
	sc.Step(`^the response contains no mapping bodies$`, func() error {
		for _, secret := range []string{"secret payload", "card=4111"} {
			if strings.Contains(string(w.last.body), secret) {
				return fmt.Errorf("the response contains %q: %s", secret, truncate(w.last.body))
			}
		}
		return nil
	})
}

// setAllowlist sets an org's IP allowlist, as the management app would.
func (w *world) setAllowlist(org string, cidrs []string, admin bool) error {
	return w.record(func() error {
		prefixes, err := server.ParsePrefixes(cidrs)
		if err != nil {
			return err
		}
		return w.app.Directory().Update(tenant.ID(org), func(o *tenant.Org) { o.Allowlist, o.AllowlistAdmin = prefixes, admin })
	})
}

// eachItemHas checks that every item of the list at path has the fields.
func (w *world) eachItemHas(path string, fields ...string) error {
	got, found, err := w.jsonOne(path)
	if err != nil {
		return err
	}
	items, _ := got.([]any) //nolint:errcheck // reported below
	if !found || len(items) == 0 {
		return fmt.Errorf("%s has no items: %s", path, truncate(w.last.body))
	}
	for i, it := range items {
		obj, _ := it.(map[string]any) //nolint:errcheck // reported below
		for _, f := range fields {
			if _, ok := obj[f]; !ok {
				return fmt.Errorf("item %d of %s lacks %q: %v", i, path, f, it)
			}
		}
	}
	return nil
}

// eachEntry runs check on every journal entry of the last response.
func (w *world) eachEntry(check func(map[string]any) error) error {
	var doc struct {
		Requests []map[string]any `json:"requests"`
	}
	if err := json.Unmarshal(w.last.body, &doc); err != nil {
		return fmt.Errorf("the response is not a journal: %w", err)
	}
	if len(doc.Requests) == 0 {
		return fmt.Errorf("the journal has no entries")
	}
	for _, e := range doc.Requests {
		if err := check(e); err != nil {
			return err
		}
	}
	return nil
}

package acceptance

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// The platform's flows (kaizentis-platform USE_CASES.md), as the engine sees them: a few admin calls and changes
// to the org's rows, in the order a customer's day brings them.

// What a support session must never be shown.
const (
	secretMappingBody  = "secret-mapping-body"
	secretRequestBody  = "secret-request-body"
	secretResponseBody = "secret-response-body"
)

const (
	validMapping = `{"request":{"method":"GET","url":"/a"},"response":{"status":200,"body":"ok"}}`
	bothURLForms = `{"request":{"method":"GET","url":"/a","urlPath":"/a"},"response":{"status":200}}`
)

// firstKey is the label of the scenario's Tool app key, when it made one.
func (m *managedWorld) firstKey() (string, error) {
	labels := make([]string, 0, len(m.keys))
	for l := range m.keys {
		labels = append(labels, l)
	}
	if len(labels) == 0 {
		return "", fmt.Errorf("the scenario made no Tool app key")
	}
	slices.Sort(labels)
	return labels[0], nil
}

// mappingStored reports whether the org's global layer holds the mapping.
func (w *world) mappingStored(org, name string) error {
	list, err := w.app.Service().Mappings.List(ctxBackground(), store.Global(tenant.ID(org)))
	if err != nil {
		return err
	}
	for _, sm := range list {
		if sm.Name == name {
			return nil
		}
	}
	return fmt.Errorf("the global layer of %s no longer has the mapping %q", org, name)
}

// answered sends a request to the engine and says how it was answered: the status and the error code.
func (w *world) answered(method, path string) (int, string, error) {
	rep, err := w.send(newRequest(method, path))
	if err != nil {
		return 0, "", err
	}
	var doc struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rep.body, &doc) //nolint:errcheck // a body that is not JSON has no error code
	return rep.status, doc.Error, nil
}

// orgSnapshot describes what the org holds in a form that two reads can be compared by.
func (w *world) orgSnapshot() (string, error) {
	m, err := w.mgd()
	if err != nil {
		return "", err
	}
	o, ok := w.app.Directory().ByID(tenant.ID(m.defaultOrg))
	if !ok {
		return "", fmt.Errorf("org %s is not in the directory", m.defaultOrg)
	}
	list, err := w.app.Service().Mappings.List(ctxBackground(), store.Global(o.ID))
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(list))
	for _, sm := range list {
		names = append(names, sm.ID+" "+sm.Name)
	}
	slices.Sort(names)
	labels := make([]string, 0, len(m.keys))
	for l := range m.keys {
		labels = append(labels, l)
	}
	slices.Sort(labels)
	var keys []string
	err = w.keepLast(func() error {
		for _, l := range labels {
			rep, callErr := w.managedAdminCall("GET", "/sessions", "", "", m.keyCred(l), nil)
			if callErr != nil {
				return callErr
			}
			keys = append(keys, fmt.Sprintf("%s=%d", l, rep.status))
		}
		return nil
	})
	return fmt.Sprintf("%+v|%v|%v", *o, names, keys), err
}

// sessionsOfLast reads the sessions of the last response by id.
func (w *world) sessionsOfLast() (map[string]map[string]any, error) {
	var doc struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil {
		return nil, fmt.Errorf("the last response is not a session list: %+v", w.last)
	}
	out := map[string]map[string]any{}
	for _, s := range doc.Sessions {
		id, _ := s["id"].(string) //nolint:errcheck // a session without an id is left out
		out[id] = s
	}
	return out, nil
}

// createWithKey makes a key-based shadow mock as a Tool app key, remembering it under name.
func (w *world) createWithKey(name, label string, ttlMinutes int) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	body := "{}"
	if ttlMinutes > 0 {
		body = fmt.Sprintf(`{"ttlSeconds":%d}`, ttlMinutes*60)
	}
	rep, err := w.managedAdminCall("POST", "/sessions/key-based", body, "", m.keyCred(label), nil)
	if err != nil {
		return err
	}
	if rep.status != http.StatusCreated {
		return fmt.Errorf("creating a shadow mock with the key %s gave %d: %s", label, rep.status, truncate(rep.body))
	}
	w.vars["pipeline.key"] = label
	return w.noteSession(name, rep)
}

func (w *world) initManagedFlows(sc *godog.ScenarioContext) {
	// F1, F30 and F32: an org is enabled, renamed, and one of several.
	sc.Step(`^the engine was not restarted$`, func() error {
		if w.starts != 1 {
			return fmt.Errorf("the engine was started %d times, want once", w.starts)
		}
		return nil
	})
	sc.Step(`^the management app writes the same org row again$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if m.snapshot, err = w.orgSnapshot(); err != nil {
			return err
		}
		o, _ := w.app.Directory().ByID(tenant.ID(m.defaultOrg))
		return w.directoryWrite(w.app.Directory().Put(*o))
	})
	sc.Step(`^the org, its mappings and its keys are unchanged$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if m.writeErr != nil {
			return fmt.Errorf("the second write was refused: %w", m.writeErr)
		}
		now, err := w.orgSnapshot()
		if err != nil {
			return err
		}
		if now != m.snapshot {
			return fmt.Errorf("writing the org again changed it:\n before %s\n after  %s", m.snapshot, now)
		}
		return nil
	})
	sc.Step(`^the org "([^"]*)" with handle "([^"]*)" on plan "([^"]*)" and a mapping for "([A-Z]+) ([^"]*)"$`, func(name, handle, plan, method, p string) error {
		if err := w.addOrg(name, handle, plan, 0, 0); err != nil {
			return err
		}
		return w.putGlobalMapping(name, method+" "+p, method, p, handle)
	})
	sc.Step(`^the org "([^"]*)" with handle "([^"]*)" on plan "([^"]*)" and a mapping "([A-Z]+) ([^"]*)" returning "([^"]*)"$`, func(name, handle, plan, method, p, body string) error {
		if err := w.addOrg(name, handle, plan, 0, 0); err != nil {
			return err
		}
		return w.putGlobalMapping(name, method+" "+p, method, p, body)
	})
	sc.Step(`^the platform renames the org's slug to "([^"]*)"$`, func(string) error { return nil }) // the engine's handle is its own
	returns := func(method, p, want string) error {
		rep, err := w.send(newRequest(method, p))
		if err != nil {
			return err
		}
		if rep.status != http.StatusOK || string(rep.body) != want {
			return fmt.Errorf("%s %s gave %d %q, want %q", method, p, rep.status, truncate(rep.body), want)
		}
		return nil
	}
	sc.Step(`^"([A-Z]+) ([^"]*)" returns "([^"]*)"$`, returns)
	sc.Step(`^"([A-Z]+) ([^"]*)" returns "([^"]*)" and "([A-Z]+) ([^"]*)" returns "([^"]*)"$`, func(m1, p1, b1, m2, p2, b2 string) error {
		if err := returns(m1, p1, b1); err != nil {
			return err
		}
		return returns(m2, p2, b2)
	})
	sc.Step(`^a key for "([^"]*)" cannot reach "([^"]*)"$`, func(own, other string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		label := "key-" + own
		err = w.addKey(label, own, []string{"sessions", "read"})
		if err != nil {
			return err
		}
		err = w.ensureOrg(other)
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("GET", "/sessions", "", other, m.keyCred(label), nil)
		if err != nil {
			return err
		}
		if rep.status != http.StatusForbidden || !strings.Contains(string(rep.body), "org_mismatch") {
			return fmt.Errorf("the key of %s on %s gave %d %s, want 403 org_mismatch", own, other, rep.status, truncate(rep.body))
		}
		return nil
	})

	// F12: a support session.
	sc.Step(`^the org has mappings with bodies, (\d+) shadow mocks and (\d+) journal entries with request bodies$`, func(sessions, entries int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		org := m.defaultOrg
		err = w.putGlobalMapping(org, "secret", "GET", "/secret", secretMappingBody)
		if err != nil {
			return err
		}
		for range sessions {
			if _, err = w.createSessionAs("harness", org); err != nil {
				return err
			}
		}
		for i := range entries {
			w.app.Service().Journal.Append(&journal.Entry{
				ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), LoggedAt: w.clk.Now().UTC(), Tenant: tenant.ID(org), Layer: "global",
				Request:  &match.Request{Method: "POST", Path: "/claims", Header: http.Header{}, Query: url.Values{}, Body: []byte(secretRequestBody)},
				Response: journal.Response{Status: 200, Body: []byte(secretResponseBody)},
			})
		}
		return nil
	})
	sc.Step(`^the audit trail has no entry for "([^"]*)"$`, func(person string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for _, e := range w.auditOf(m.defaultOrg) {
			if e.Subject == person {
				return fmt.Errorf("the audit trail has an entry for %q: %+v", person, e)
			}
		}
		return nil
	})
	sc.Step(`^the response holds none of the org's bodies$`, func() error {
		for _, secret := range []string{secretMappingBody, secretRequestBody, secretResponseBody} {
			if strings.Contains(string(w.last.body), secret) {
				return fmt.Errorf("the response holds %q: %s", secret, truncate(w.last.body))
			}
		}
		return nil
	})

	// F13 and F17: deletion and leaving.
	sc.Step(`^at once the SUT, its keys and its tokens are answered (\d+)$`, func(code int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		status, _, err := w.answered("GET", "/"+m.orgs[m.defaultOrg].handle+"/hello")
		if err != nil {
			return err
		}
		if status != code {
			return fmt.Errorf("the SUT's request gave %d, want %d", status, code)
		}
		return w.keepLast(func() error {
			creds := map[string]*credential{"token": w.setupCred(m.defaultOrg)}
			for label := range m.keys {
				creds["key "+label] = m.keyCred(label)
			}
			for what, cred := range creds {
				rep, callErr := w.managedAdminCall("GET", "/sessions", "", "", cred, nil)
				if callErr != nil {
					return callErr
				}
				if rep.status != code {
					return fmt.Errorf("the %s gave %d, want %d", what, rep.status, code)
				}
			}
			return nil
		})
	})
	sc.Step(`^the handle "([^"]*)" is still reserved$`, func(handle string) error {
		err := w.app.Directory().Put(tenant.Org{ID: "someone-else", Handle: handle})
		if !errors.Is(err, tenant.ErrHandleTaken) && !errors.Is(err, tenant.ErrHandleReserved) {
			return fmt.Errorf("another org could take the handle %q: %v", handle, err)
		}
		return nil
	})
	sc.Step(`^the engine still answers (\d+) for org "([^"]*)"$`, func(code int, org string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		status, _, err := w.answered("GET", "/"+m.orgs[org].handle+"/hello")
		if err != nil {
			return err
		}
		if status != code {
			return fmt.Errorf("org %s answers %d, want %d", org, status, code)
		}
		return nil
	})
	sc.Step(`^a Tool app key "([^"]*)" made by "([^"]*)" for org "([^"]*)"$`, func(label, _, org string) error {
		return w.addKey(label, org, []string{"sessions", "read"})
	})
	sc.Step(`^the key still authenticates until the management app revokes or disables it$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		label, err := m.firstKey()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("GET", "/sessions", "", "", m.keyCred(label), nil)
		if err != nil {
			return err
		}
		if rep.status != http.StatusOK {
			return fmt.Errorf("the key gave %d after its maker was anonymised: %s", rep.status, truncate(rep.body))
		}
		return nil
	})

	// F4, F5, F6, F8, F10 and F19: a pipeline and its keys.
	sc.Step(`^I call admin "([A-Z]+)" "([^"]*)" with the key "([^"]*)" and a (valid mapping|mapping with both url and urlPath)$`, func(method, p, label, kind string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		body := validMapping
		if kind != "valid mapping" {
			body = bothURLForms
		}
		_, err = w.managedAdminCall(method, p, body, "", m.keyCred(label), nil)
		return err
	})
	sc.Step(`^the response says which field is wrong$`, func() error {
		if !strings.Contains(string(w.last.body), "url") {
			return fmt.Errorf("the response does not name the field: %s", truncate(w.last.body))
		}
		return nil
	})
	sc.Step(`^nothing was stored$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		list, err := w.app.Service().Mappings.List(ctxBackground(), store.Global(tenant.ID(m.defaultOrg)))
		if err != nil {
			return err
		}
		if len(list) != 0 {
			return fmt.Errorf("the global layer holds %d mappings, want none", len(list))
		}
		return nil
	})
	sc.Step(`^the global layer has "([A-Z]+) ([^"]*)" returning "([^"]*)"$`, func(method, p, body string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.putGlobalMapping(m.defaultOrg, method+" "+p, method, p, body)
	})
	sc.Step(`^the global layer has "([^"]*)" for "([A-Z]+) ([^"]*)" with metadata source "([^"]*)"$`, func(name, method, p, src string) error {
		return w.writeLayerMapping(layerMapping{name: name, method: method, url: p, metadata: source(src)})
	})
	sc.Step(`^the pipeline creates a key-based shadow mock "([^"]*)" with the key "([^"]*)" and a time to live of (\d+) minutes$`, func(name, label string, mins int) error {
		return w.createWithKey(name, label, mins)
	})
	sc.Step(`^the pipeline created "([^"]*)" with the key "([^"]*)"$`, func(name, label string) error {
		return w.createWithKey(name, label, 0)
	})
	sc.Step(`^a shadow mock "([^"]*)" made with the key$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		label, err := m.firstKey()
		if err != nil {
			return err
		}
		return w.createWithKey(name, label, 0)
	})
	mapOf := func(method, p, body string) string {
		return fmt.Sprintf(`{"request":{"method":%q,"url":%q},"response":{"status":200,"body":%q}}`, method, p, body)
	}
	sc.Step(`^adds a mapping to "([^"]*)" for "([A-Z]+) ([^"]*)" returning "([^"]*)"$`, func(name, method, p, body string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("POST", "/sessions/"+w.vars[name+".id"]+"/mappings", mapOf(method, p, body), "", m.keyCred(w.vars["pipeline.key"]), nil)
		if err != nil {
			return err
		}
		if rep.status != http.StatusCreated {
			return fmt.Errorf("adding the mapping gave %d: %s", rep.status, truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^I add the mapping to "([^"]*)" through the admin API with the key$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		_, err = w.managedAdminCall("POST", "/sessions/"+w.vars[name+".id"]+"/mappings", validMapping, "", m.keyCred(w.vars["pipeline.key"]), nil)
		return err
	})
	sc.Step(`^the pipeline deletes "([^"]*)" with the key "([^"]*)"$`, func(name, label string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("DELETE", "/sessions/"+w.vars[name+".id"], "", "", m.keyCred(label), nil)
		if err != nil {
			return err
		}
		if rep.status/100 != 2 {
			return fmt.Errorf("deleting the shadow mock gave %d: %s", rep.status, truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^the pipeline created a shadow mock "([^"]*)" with a time to live of (\d+) minutes and then stopped$`, func(name string, mins int) error {
		return w.createSession("key-based", name, fmt.Sprintf(`{"ttlSeconds":%d}`, mins*60))
	})
	sc.Step(`^(\d+) minutes pass$`, func(n int) error { w.clk.advance(time.Duration(n) * time.Minute); return nil })
	sc.Step(`^"([^"]*)" is gone$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("GET", "/sessions/"+w.vars[name+".id"], "", "", w.setupCred(m.defaultOrg), nil)
		if err != nil {
			return err
		}
		if rep.status != http.StatusNotFound {
			return fmt.Errorf("the shadow mock %s gave %d, want it gone", name, rep.status)
		}
		return nil
	})
	sc.Step(`^it no longer counts as an active session in the usage$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.app.Service().ReapOnce(ctxBackground())
		if n := len(w.app.Service().Sessions.ListAll(tenant.ID(m.defaultOrg))); n != 0 {
			return fmt.Errorf("the org still has %d shadow mocks", n)
		}
		return w.keepLast(func() error {
			rep, callErr := w.managedAdminCall("GET", "/stats/live", "", "", w.setupCred(m.defaultOrg), nil)
			if callErr != nil {
				return callErr
			}
			var doc struct {
				Active struct {
					KeyBased int `json:"keyBased"`
					URLBased int `json:"urlBased"`
				} `json:"activeSessions"`
			}
			if json.Unmarshal(rep.body, &doc) != nil || doc.Active.KeyBased+doc.Active.URLBased != 0 {
				return fmt.Errorf("the live stats still count active sessions: %s", truncate(rep.body))
			}
			return nil
		})
	})
	sc.Step(`^a member lists the shadow mocks$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.adminAs("GET", "/sessions", "erin", m.defaultOrg, "member")
	})
	sc.Step(`^"([^"]*)" shows the creator "([^"]*)" and "([^"]*)" shows "([^"]*)"$`, func(s1, key, s2, person string) error {
		sessions, err := w.sessionsOfLast()
		if err != nil {
			return err
		}
		if got := sessions[w.vars[s1+".id"]]["createdByKey"]; got != key {
			return fmt.Errorf("%s shows the key %v, want %q", s1, got, key)
		}
		if got := sessions[w.vars[s2+".id"]]["createdBy"]; got != person {
			return fmt.Errorf("%s shows the creator %v, want %q", s2, got, person)
		}
		return nil
	})
	sc.Step(`^the SUT sends "([A-Z]+) ([^"]*)" without the key$`, func(method, p string) error {
		_, err := w.send(newRequest(method, p))
		return err
	})
	sc.Step(`^a shadow mock "([^"]*)"$`, func(name string) error { return w.newSession(name) })
	sc.Step(`^a member searches the journal for unmatched requests of "([^"]*)"$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.adminAs("GET", "/requests?matched=false&session="+w.vars[name+".id"], "erin", m.defaultOrg, "member")
	})
	sc.Step(`^the entry is listed with its full request and the response the engine gave$`, func() error {
		var doc struct {
			Requests []struct {
				Request struct {
					Method string `json:"method"`
					URL    string `json:"url"`
				} `json:"request"`
				Response struct {
					Status int `json:"status"`
				} `json:"response"`
			} `json:"requests"`
		}
		if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil || len(doc.Requests) != 1 {
			return fmt.Errorf("the journal search gave %s, want the one unmatched request", truncate(w.last.body))
		}
		e := doc.Requests[0]
		if e.Request.Method != http.MethodGet || !strings.HasSuffix(e.Request.URL, "/claims/2") || e.Response.Status != http.StatusNotFound {
			return fmt.Errorf("the entry is %+v, want GET /claims/2 answered 404", e)
		}
		return nil
	})
	sc.Step(`^the member opens its near-miss analysis of "([^"]*)"$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.adminAs("GET", "/sessions/"+w.vars[name+".id"]+"/requests/unmatched/near-misses", "erin", m.defaultOrg, "member")
	})
	sc.Step(`^"([^"]*)" is the closest candidate, with why it missed and its source "([^"]*)"$`, func(name, src string) error {
		var doc struct {
			NearMisses []struct {
				StubMapping struct {
					Name string `json:"name"`
				} `json:"stubMapping"`
				Source      string         `json:"source"`
				MatchResult map[string]any `json:"matchResult"`
			} `json:"nearMisses"`
		}
		if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil || len(doc.NearMisses) == 0 {
			return fmt.Errorf("the near-miss analysis gave %s", truncate(w.last.body))
		}
		first := doc.NearMisses[0]
		if first.StubMapping.Name != name || first.Source != src || len(first.MatchResult) == 0 {
			return fmt.Errorf("the closest candidate is %+v, want %q from %q with why it missed", first, name, src)
		}
		return nil
	})
	sc.Step(`^calls with the key are refused with (\d+)$`, func(code int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		label, err := m.firstKey()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("GET", "/sessions", "", "", m.keyCred(label), nil)
		if err != nil {
			return err
		}
		if rep.status != code {
			return fmt.Errorf("a call with the revoked key gave %d, want %d", rep.status, code)
		}
		return nil
	})
	sc.Step(`^the key's last used time and address "([^"]*)" are in the key-usage table$`, func(ip string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		label, err := m.firstKey()
		if err != nil {
			return err
		}
		use, ok := w.app.Keys().LastUse(auth.HashKey(m.keys[label]))
		if !ok || use.IP != ip || use.At.IsZero() {
			return fmt.Errorf("last use = %+v (recorded: %v), want the address %s", use, ok, ip)
		}
		return nil
	})

	// F19: a recording a pipeline makes.
	sc.Step(`^an upstream test server returning "([^"]*)" for "([^"]*)"$`, func(body, route string) error {
		// The test server is on this machine, which managed mode refuses, so this scenario lets private addresses through.
		if err := w.reconfigure("egress:\n  blockPrivate: false\n"); err != nil {
			return err
		}
		w.stubUpstream("pipeline", route, canned{status: 200, body: body})
		return nil
	})
	sc.Step(`^the pipeline starts a recording into "([^"]*)" from the upstream with the key$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		_, err = w.managedAdminCall("POST", "/sessions/"+w.vars[name+".id"]+"/recordings/start", fmt.Sprintf(`{"targetBaseUrl":%q}`, w.vars["upstream.pipeline"]), "", m.keyCred(w.vars["pipeline.key"]), nil)
		if err == nil && w.last.status/100 != 2 {
			err = fmt.Errorf("starting the recording gave %d: %s", w.last.status, truncate(w.last.body))
		}
		return err
	})
	sc.Step(`^the pipeline stops the recording$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		_, err = w.managedAdminCall("POST", "/sessions/"+w.vars["T1.id"]+"/recordings/stop", "", "", m.keyCred(w.vars["pipeline.key"]), nil)
		if err == nil && w.last.status/100 != 2 {
			err = fmt.Errorf("stopping the recording gave %d: %s", w.last.status, truncate(w.last.body))
		}
		return err
	})
	sc.Step(`^a member reads the mappings and files of "([^"]*)"$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for _, part := range []string{"mappings", "files"} {
			rep, callErr := w.managedAdminCall("GET", "/sessions/"+w.vars[name+".id"]+"/"+part, "", "", m.keyCred(w.vars["pipeline.key"]), nil)
			if callErr != nil {
				return callErr
			}
			if rep.status != http.StatusOK {
				return fmt.Errorf("reading the %s gave %d: %s", part, rep.status, truncate(rep.body))
			}
			w.vars["read."+part] = string(rep.body)
		}
		return nil
	})
	sc.Step(`^the recorded mapping and the file holding "([^"]*)" are returned$`, func(payload string) error {
		if !strings.Contains(w.vars["read.mappings"], "/pay/1") {
			return fmt.Errorf("the mappings hold no recording of /pay/1: %s", w.vars["read.mappings"])
		}
		// A small body is kept in the mapping, a large one in a file.
		if !strings.Contains(w.vars["read.mappings"], payload) && !strings.Contains(w.vars["read.files"], payload) {
			var files struct {
				Files []string `json:"files"`
			}
			if json.Unmarshal([]byte(w.vars["read.files"]), &files) != nil || len(files.Files) == 0 {
				return fmt.Errorf("neither the mapping nor a file holds %q: %s %s", payload, w.vars["read.mappings"], w.vars["read.files"])
			}
		}
		return nil
	})
	sc.Step(`^the shadow mock is unchanged and still expires on its time to live$`, func() error {
		rep, err := w.mustAdmin("GET", "/sessions/${T1.id}", "", 200)
		if err != nil {
			return err
		}
		var doc struct {
			Kind      string `json:"kind"`
			ExpiresAt string `json:"expiresAt"`
		}
		if json.Unmarshal(rep.body, &doc) != nil || doc.Kind != "key-based" || doc.ExpiresAt == "" {
			return fmt.Errorf("the shadow mock is %s, want a key-based one with an expiry", truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^I start a recording into "([^"]*)" from "([^"]*)"$`, func(name, target string) error {
		_, err := w.mustAdmin("POST", "/sessions/${"+name+".id}/recordings/start", fmt.Sprintf(`{"targetBaseUrl":%q}`, target), 200, 201, 202, 204)
		return err
	})

	// F9, F15, F16, F18, F22 and F24: plans, limits and holds.
	sc.Step(`^the org is limited to (\d+) requests per second$`, func(rps int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		return w.limitOrg(m.defaultOrg, func(l *tenant.Limits) { l.RequestsPerSecond, l.Burst = float64(rps), rps })
	})
	sc.Step(`^a test sends (\d+) requests in one second$`, func(n int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		return w.sendBurst(n, "GET", "/"+m.orgs[m.defaultOrg].handle+"/hello")
	})
	sc.Step(`^most are answered 429 with "([^"]*)", "([^"]*)" and "([^"]*)"$`, func(code, h1, h2 string) error {
		limited := 0
		for _, r := range w.batch {
			if r.status != http.StatusTooManyRequests {
				continue
			}
			limited++
			var doc struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(r.body, &doc) != nil || doc.Error != code || r.header.Get(h1) == "" || r.header.Get(h2) == "" {
				return fmt.Errorf("a 429 is %s with the headers %v, want %q with %s and %s", truncate(r.body), r.header, code, h1, h2)
			}
		}
		if limited*2 <= len(w.batch) {
			return fmt.Errorf("only %d of %d requests were answered 429", limited, len(w.batch))
		}
		return nil
	})
	sc.Step(`^"([A-Z]+) ([^"]*)" for another org is unaffected$`, func(method, p string) error { return w.served(method, p) })
	sc.Step(`^(\d+) requests were rate limited in the last hour$`, func(n int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		for range n {
			w.app.Service().Usage.RateLimited(tenant.ID(m.defaultOrg))
		}
		return nil
	})
	sc.Step(`^the current hour's "([^"]*)" is (\d+)$`, func(field string, want int) error {
		b, err := w.bucketAt(w.clk.Now())
		if err != nil {
			return err
		}
		got := map[string]int64{"rateLimited": b.RateLimited, "ipRejected": b.IPRejected, "requests": b.Requests}
		v, known := got[field]
		if !known || v != int64(want) {
			return fmt.Errorf("the current hour's %s is %d (known: %v), want %d", field, v, known, want)
		}
		return nil
	})
	sc.Step(`^"([A-Z]+) ([^"]*)" is answered (\d+) with "([^"]*)"$`, func(method, p string, code int, errCode string) error {
		status, got, err := w.answered(method, p)
		if err != nil {
			return err
		}
		if status != code || got != errCode {
			return fmt.Errorf("%s %s gave %d %q, want %d %q", method, p, status, got, code, errCode)
		}
		return nil
	})
	sc.Step(`^"([A-Z]+) ([^"]*)" is answered (\d+)$`, func(method, p string, code int) error {
		status, _, err := w.answered(method, p)
		if err != nil {
			return err
		}
		if status != code {
			return fmt.Errorf("%s %s gave %d, want %d", method, p, status, code)
		}
		return nil
	})
	sc.Step(`^no mapping, shadow mock or setting was lost$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if err := w.mappingStored(m.defaultOrg, "GET /hello"); err != nil {
			return err
		}
		if o, ok := w.app.Directory().ByID(tenant.ID(m.defaultOrg)); !ok || o.Limits != planLimit("team") {
			return fmt.Errorf("the org is %+v, want its Team limits kept", o)
		}
		return nil
	})
	sc.Step(`^the org's mappings are still stored$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.mappingStored(m.defaultOrg, "GET /hello")
	})
	sc.Step(`^the org's configuration is kept$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		o, ok := w.app.Directory().ByID(tenant.ID(m.defaultOrg))
		if !ok || o.Handle != m.orgs[m.defaultOrg].handle || o.Limits != planLimit("team") {
			return fmt.Errorf("the org is %+v, want its handle and limits kept", o)
		}
		return w.mappingStored(m.defaultOrg, "GET /hello")
	})
	sc.Step(`^the management app subscribes the org again within 30 days$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.directoryWrite(w.app.Directory().SetStatus(tenant.ID(m.defaultOrg), tenant.StatusActive))
	})
	sc.Step(`^the management app marks org "([^"]*)" suspended for an administrative hold$`, func(org string) error {
		return w.directoryWrite(w.app.Directory().SetStatus(tenant.ID(org), tenant.StatusSuspended))
	})
	sc.Step(`^its Tool app keys and tokens are refused$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.keepLast(func() error {
			creds := map[string]*credential{"token": w.setupCred(m.defaultOrg)}
			for label := range m.keys {
				creds["key "+label] = m.keyCred(label)
			}
			for what, cred := range creds {
				rep, callErr := w.managedAdminCall("GET", "/sessions", "", "", cred, nil)
				if callErr != nil {
					return callErr
				}
				if rep.status != http.StatusForbidden {
					return fmt.Errorf("the %s gave %d, want it refused", what, rep.status)
				}
			}
			return nil
		})
	})
}

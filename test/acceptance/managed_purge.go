package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/audit"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// The audit trail, purging an org and anonymising a person (FR-11.15, FR-11.5).

// unknownSession is a shadow mock id that does not exist: a call on it is refused, and still audited.
const unknownSession = "ses_00000000000000000000000000"

// systemCred is how the management app calls for its own purposes: a token with the system scope.
func (w *world) systemCred(org string) *credential {
	return &credential{org: org, bearer: func() (string, error) {
		sp := w.tokenOf("management-app", org, "admin")
		sp.scope = "system"
		return w.m.mint(w, sp), nil
	}}
}

// auditOf lists the org's audit entries, newest first.
func (w *world) auditOf(org string) []audit.Entry {
	return w.app.Audit().List(ctxBackground(), tenant.ID(org))
}

// createSessionAs makes a key-based shadow mock as a person, and returns its id.
func (w *world) createSessionAs(person, org string) (string, error) {
	m, err := w.mgd()
	if err != nil {
		return "", err
	}
	sp := w.tokenOf(person, org, "member")
	rep, err := w.managedAdminCall("POST", "/sessions/key-based", "{}", org, &credential{org: org, bearer: func() (string, error) { return m.mint(w, sp), nil }}, nil)
	if err != nil {
		return "", err
	}
	var doc struct {
		ID string `json:"id"`
	}
	if rep.status != http.StatusCreated || json.Unmarshal(rep.body, &doc) != nil {
		return "", fmt.Errorf("creating a shadow mock gave %d: %s", rep.status, truncate(rep.body))
	}
	return doc.ID, nil
}

// asManagementApp makes an admin call with the system scope on the scenario's org.
func (w *world) asManagementApp(method, path string) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	_, err = w.managedAdminCall(method, path, "", m.defaultOrg, w.systemCred(m.defaultOrg), nil)
	return err
}

func (w *world) purgeReports(status string) error {
	var doc struct {
		Status string `json:"status"`
	}
	if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil || doc.Status != status {
		return fmt.Errorf("the purge answered %+v, want the status %q", w.last, status)
	}
	return nil
}

func (w *world) initManagedPurge(sc *godog.ScenarioContext) {
	// The trail.
	sc.Step(`^the audit trail has an entry with the org, issuer, "([^"]*)", the token's id, the route, the status and the time$`, func(sub string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for _, e := range w.auditOf(m.defaultOrg) {
			if e.Subject == sub && e.Org == tenant.ID(m.defaultOrg) && e.Issuer != "" && e.TokenID != "" && e.Method != "" && e.Route != "" && e.Status != 0 && !e.At.IsZero() {
				return nil
			}
		}
		return fmt.Errorf("no complete audit entry for %q in %+v", sub, w.auditOf(m.defaultOrg))
	})
	sc.Step(`^the audit trail has an entry for "([^"]*)"$`, func(label string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for _, e := range w.auditOf(m.defaultOrg) {
			if e.Key == label {
				return nil
			}
		}
		return fmt.Errorf("no audit entry has the key %q in %+v", label, w.auditOf(m.defaultOrg))
	})
	sc.Step(`^the audit trail has no new entry$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if es := w.auditOf(m.defaultOrg); len(es) != 0 {
			return fmt.Errorf("the audit trail has %d entries, want none: %+v", len(es), es)
		}
		return nil
	})

	// Purging.
	sc.Step(`^the org has (\d+) shadow mocks, call counters, (\d+) journal entries, stats rows and a recording$`, func(n, entries int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		org := m.defaultOrg
		for i := range n {
			id, createErr := w.createSessionAs("harness", org)
			if createErr != nil {
				return createErr
			}
			if i == 0 {
				m.recordedSession = id
			}
		}
		rep, err := w.managedAdminCall("POST", "/sessions/"+m.recordedSession+"/recordings/start", `{"targetBaseUrl":"http://example.com"}`, org, w.setupCred(org), nil)
		if err != nil {
			return err
		}
		if rep.status/100 != 2 {
			return fmt.Errorf("starting a recording gave %d: %s", rep.status, truncate(rep.body))
		}
		global := store.Global(tenant.ID(org))
		if err = w.app.Service().Counters.Set(ctxBackground(), counter.Key{RequestScope: global, StubScope: global, StubID: "stub-1"}, 4); err != nil {
			return err
		}
		if err = w.putGlobalMapping(org, "hello", "GET", "/hello", "hello"); err != nil {
			return err
		}
		for range entries {
			if err = w.sendOne(newRequest("GET", "/"+m.orgs[org].handle+"/hello")); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the management app calls admin "([^"]*)" "/org/purge" for org "([^"]*)"(?: again)?$`, func(method, org string) error {
		_, err := w.managedAdminCall(method, "/org/purge", "", org, w.systemCred(org), nil)
		return err
	})
	sc.Step(`^the management app purges the org$`, func() error { return w.asManagementApp("POST", "/org/purge") })
	sc.Step(`^the org was purged$`, func() error {
		return w.keepLast(func() error { return w.asManagementApp("POST", "/org/purge") })
	})
	sc.Step(`^the management app marked the org as deleted and purged it$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if err = w.record(func() error { return w.app.Directory().MarkDeleted(tenant.ID(m.defaultOrg)) }); err != nil {
			return err
		}
		return w.keepLast(func() error { return w.asManagementApp("POST", "/org/purge") })
	})
	sc.Step(`^the shadow mocks, Redis keys, journal rows and stats rows for org "([^"]*)" are deleted$`, func(org string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		svc := w.app.Service()
		id := tenant.ID(org)
		global := store.Global(id)
		if n := len(svc.Sessions.ListAll(id)); n != 0 {
			return fmt.Errorf("org %s still has %d shadow mocks", org, n)
		}
		if cs, listErr := svc.Counters.List(ctxBackground(), global); listErr != nil || len(cs) != 0 {
			return fmt.Errorf("org %s still has counters: %v %v", org, cs, listErr)
		}
		if n := len(svc.Journal.Query(global, nil, 0)); n != 0 {
			return fmt.Errorf("org %s still has %d journal entries", org, n)
		}
		live := svc.Stats.Live(id, "1h", time.Hour, w.clk.Now(), stats.ActiveSessions{})
		if len(live.RequestsPerMinute) != 0 || live.Unmatched != 0 {
			return fmt.Errorf("org %s still has stats: %+v", org, live)
		}
		if a := svc.Recorder.Find([]store.Scope{{Tenant: id, SessionID: m.recordedSession}}); a != nil {
			return fmt.Errorf("the recording of the purged shadow mock is still running")
		}
		return nil
	})
	sc.Step(`^the purge reports its progress until it is done$`, func() error { return w.purgeReports("done") })
	sc.Step(`^the purge reports "([^"]*)"$`, w.purgeReports)
	sc.Step(`^no journal entry for org "([^"]*)" is readable$`, func(org string) error {
		if n := len(w.app.Service().Journal.Query(store.Global(tenant.ID(org)), nil, 0)); n != 0 {
			return fmt.Errorf("org %s still has %d journal entries", org, n)
		}
		return nil
	})

	// Who may purge.
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with a token for "([^"]*)" in org "([^"]*)" with app role "([^"]*)"$`, func(method, p, sub, org, role string) error {
		return w.adminAs(method, p, sub, org, role)
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with a Tool app key$`, func(method, p string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if _, ok := m.keys["ci"]; !ok {
			if keyErr := w.addKey("ci", m.defaultOrg, []string{"sessions", "read"}); keyErr != nil {
				return keyErr
			}
		}
		_, err = w.managedAdminCall(method, p, "", m.defaultOrg, m.keyCred("ci"), nil)
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" for org "([^"]*)" with a token of scope "([^"]*)" in org "([^"]*)"$`, func(method, p, target, scope, own string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if err = w.ensureOrg(target); err != nil {
			return err
		}
		sp := w.tokenOf("management-app", own, "admin")
		sp.scope = scope
		_, err = w.managedAdminCall(method, p, "", target, &credential{org: own, bearer: func() (string, error) { return m.mint(w, sp), nil }}, nil)
		return err
	})

	// Anonymising.
	sc.Step(`^"([^"]*)" created (\d+) shadow mocks and made (\d+) audited calls$`, func(person string, n, calls int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for range n {
			if _, createErr := w.createSessionAs(person, m.defaultOrg); createErr != nil {
				return createErr
			}
		}
		for range calls - n {
			if err = w.adminAs("DELETE", "/sessions/"+unknownSession, person, m.defaultOrg, "member"); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the management app calls admin "([^"]*)" "/people/([^"/]*)/anonymise"(?: again)?$`, func(method, person string) error {
		return w.asManagementApp(method, "/people/"+person+"/anonymise")
	})
	sc.Step(`^"([^"]*)" has been anonymised$`, func(person string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		// erin made a call too, so that "leaves others alone" has something to check.
		return w.keepLast(func() error {
			for _, who := range []string{"erin", person} {
				if callErr := w.adminAs("DELETE", "/sessions/"+unknownSession, who, m.defaultOrg, "member"); callErr != nil {
					return callErr
				}
			}
			return w.asManagementApp("POST", "/people/"+person+"/anonymise")
		})
	})
	sc.Step(`^the audit trail still has the (\d+) entries$`, func(want int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		n := 0
		for _, e := range w.auditOf(m.defaultOrg) {
			if !strings.Contains(e.Route, "/people/") {
				n++
			}
		}
		if n != want {
			return fmt.Errorf("the audit trail has %d entries, want %d", n, want)
		}
		return nil
	})
	sc.Step(`^their subject is an anonymous marker$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for _, e := range w.auditOf(m.defaultOrg) {
			if !strings.Contains(e.Route, "/people/") && e.Subject != audit.Anonymous {
				return fmt.Errorf("the entry %+v still has a subject", e)
			}
		}
		return nil
	})
	sc.Step(`^the (\d+) shadow mocks show the same anonymous marker as their creator$`, func(want int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		n := 0
		for _, s := range w.app.Service().Sessions.ListAll(tenant.ID(m.defaultOrg)) {
			if s.CreatedBy == audit.Anonymous {
				n++
			}
		}
		if n != want {
			return fmt.Errorf("%d shadow mocks have the anonymous marker as their creator, want %d", n, want)
		}
		return nil
	})
	sc.Step(`^no entry names "([^"]*)"$`, func(person string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for _, e := range w.auditOf(m.defaultOrg) {
			if strings.Contains(fmt.Sprintf("%+v", e), person) {
				return fmt.Errorf("the entry %+v names %q", e, person)
			}
		}
		return nil
	})
	sc.Step(`^entries for "([^"]*)" are unchanged$`, func(person string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for _, e := range w.auditOf(m.defaultOrg) {
			if e.Subject == person {
				return nil
			}
		}
		return fmt.Errorf("no entry names %q any more", person)
	})
}

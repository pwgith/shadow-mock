package acceptance

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// writeCode names the reason a write to the org directory was refused, as the management app would see it.
func writeCode(err error) string {
	switch {
	case err == nil:
		return "accepted"
	case errors.Is(err, tenant.ErrHandleInvalid):
		return "handle_invalid"
	case errors.Is(err, tenant.ErrHandleReserved):
		return "handle_reserved"
	case errors.Is(err, tenant.ErrHandleTaken):
		return "handle_taken"
	case errors.Is(err, tenant.ErrDeleted):
		return "org_deleted"
	default:
		return err.Error()
	}
}

// directoryWrite is an org write the management app made, kept so that a later step can say how it went.
func (w *world) directoryWrite(err error) error {
	m, mErr := w.mgd()
	if mErr != nil {
		return mErr
	}
	m.writeErr = err
	return nil
}

// served reports whether a request to the engine got an answer from a mapping and not from the engine's own gates.
func (w *world) served(method, path string) error {
	rep, err := w.send(newRequest(method, path))
	if err != nil {
		return err
	}
	if rep.status != http.StatusOK {
		return fmt.Errorf("%s %s gave %d, want it served: %s", method, path, rep.status, truncate(rep.body))
	}
	return nil
}

func (w *world) initManagedTenancy(sc *godog.ScenarioContext) {
	// Org status, as the management app changes it.
	setStatus := func(org string, status tenant.Status) error {
		return w.directoryWrite(w.app.Directory().SetStatus(tenant.ID(org), status))
	}
	sc.Step(`^the management app sets the status of org "([^"]*)" to "([^"]*)"$`, func(org, status string) error {
		return setStatus(org, tenant.Status(status))
	})
	sc.Step(`^the status of org "([^"]*)" is "([^"]*)"$`, func(org, status string) error {
		return w.record(func() error { return w.app.Directory().SetStatus(tenant.ID(org), tenant.Status(status)) })
	})
	sc.Step(`^the management app marks org "([^"]*)" as deleted$`, func(org string) error {
		return w.record(func() error { return w.app.Directory().MarkDeleted(tenant.ID(org)) })
	})
	sc.Step(`^a late write sets the status of org "([^"]*)" to "([^"]*)"$`, func(org, status string) error {
		return setStatus(org, tenant.Status(status))
	})
	sc.Step(`^org "([^"]*)" is still deleted$`, func(org string) error {
		if o, ok := w.app.Directory().ByID(tenant.ID(org)); !ok || o.Status != tenant.StatusDeleted {
			return fmt.Errorf("org %s is %+v, want it deleted", org, o)
		}
		return nil
	})
	sc.Step(`^the key "([^"]*)" is refused with 410$`, func(label string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("GET", "/sessions", "", "", m.keyCred(label), nil)
		if err != nil {
			return err
		}
		if rep.status != http.StatusGone {
			return fmt.Errorf("the key gave %d, want 410: %s", rep.status, truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^the mapping "([^"]*)" is still there$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		list, err := w.app.Service().Mappings.List(ctxBackground(), store.Global(tenant.ID(m.defaultOrg)))
		if err != nil {
			return err
		}
		for _, sm := range list {
			if sm.Name == name {
				return nil
			}
		}
		return fmt.Errorf("the global layer of %s no longer has the mapping %q", m.defaultOrg, name)
	})
	sc.Step(`^no session, counter, journal row or stats row exists for org "([^"]*)"$`, func(org string) error {
		svc := w.app.Service()
		scope := store.Global(tenant.ID(org))
		if n := len(svc.Sessions.ListAll(tenant.ID(org))); n != 0 {
			return fmt.Errorf("org %s has %d sessions", org, n)
		}
		if cs, err := svc.Counters.List(ctxBackground(), scope); err != nil || len(cs) != 0 {
			return fmt.Errorf("org %s has counters: %v %v", org, cs, err)
		}
		if n := len(svc.Journal.Query(scope, nil, 0)); n != 0 {
			return fmt.Errorf("org %s has %d journal entries", org, n)
		}
		return nil
	})

	// "Served" means the engine's own gates let it through and a mapping answered.
	sc.Step(`^"([A-Z]+) ([^"]*)" is served$`, func(method, path string) error { return w.served(method, path) })
	sc.Step(`^within (\d+) seconds? "([A-Z]+) ([^"]*)" is served$`, func(secs int, method, path string) error {
		return w.eventually(time.Duration(secs)*time.Second, func() error { return w.served(method, path) })
	})

	// Limits that come from the plan.
	sc.Step(`^the plan's monthly request cap is (\d+) with a grace of (\d+)$`, func(capN, grace int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.record(func() error {
			return w.app.Directory().Update(tenant.ID(m.defaultOrg), func(o *tenant.Org) {
				o.Limits.MonthlyRequestCap, o.Limits.Grace = int64(capN), int64(grace)
			})
		})
	})
	sc.Step(`^the org has served (\d+) requests this month$`, func(n int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.meterRequests(m.defaultOrg, n)
		return nil
	})
	sc.Step(`^the org is on a plan with a soft cap$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.record(func() error {
			return w.app.Directory().Update(tenant.ID(m.defaultOrg), func(o *tenant.Org) { o.Limits.MonthlyRequestCap = 0 })
		})
	})
	sc.Step(`^the org has served more requests than the plan includes$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.meterRequests(m.defaultOrg, 5000)
		return nil
	})

	// Handles and aliases.
	sc.Step(`^the management app creates org "([^"]*)" with handle "([^"]*)"$`, func(id, handle string) error {
		if err := w.ensure(); err != nil {
			return err
		}
		return w.directoryWrite(w.app.Directory().Put(tenant.Org{ID: tenant.ID(id), Handle: handle}))
	})
	sc.Step(`^the write is accepted$`, func() error { return w.writeIs("accepted") })
	sc.Step(`^the write is refused as "([^"]*)"$`, func(code string) error { return w.writeIs(code) })
	sc.Step(`^the change is rejected with "([^"]*)"$`, func(reason string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if m.writeErr == nil {
			return fmt.Errorf("the change was accepted, want it rejected with %q", reason)
		}
		if got := strings.ToLower(m.writeErr.Error()); !strings.Contains(got, strings.ToLower(reason)) {
			return fmt.Errorf("the change was rejected with %q, want it to say %q", m.writeErr, reason)
		}
		return nil
	})
	sc.Step(`^the org "([^"]*)" with handle "([^"]*)" and the alias "([^"]*)"$`, func(name, handle, alias string) error {
		if err := w.addOrg(name, handle, "Team", 0, 0); err != nil {
			return err
		}
		return w.record(func() error { return w.app.Directory().AddAlias(tenant.ID(name), alias) })
	})
	sc.Step(`^it reaches org "([^"]*)"$`, func(string) error {
		var doc struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(w.last.body, &doc) == nil && doc.Error == "unknown_org" {
			return fmt.Errorf("the request did not reach an org: %s", truncate(w.last.body))
		}
		return nil
	})
	sc.Step(`^the alias cannot be removed through the engine's configuration tables$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		// Writing the org again, without its aliases, is the nearest thing to removing one.
		o, _ := w.app.Directory().ByID(tenant.ID(m.defaultOrg)) //nolint:errcheck // the org exists
		if err = w.app.Directory().Put(tenant.Org{ID: o.ID, Handle: o.Handle}); err != nil {
			return err
		}
		if _, ok := w.app.Directory().Lookup("acme"); !ok {
			return fmt.Errorf("the alias no longer resolves")
		}
		return nil
	})
	sc.Step(`^the management app writes mapping "([^"]*)" for "([A-Z]+) ([^"]*)" returning "([^"]*)"$`, func(name, method, url, body string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.putGlobalMapping(m.defaultOrg, name, method, url, body, nil)
	})
	sc.Step(`^I request "([^"]*)"$`, func(path string) error { _, err := w.send(newRequest("GET", path)); return err })
	sc.Step(`^the response is not "([^"]*)"$`, func(not string) error {
		if string(w.last.body) == not {
			return fmt.Errorf("the response is %q", not)
		}
		return nil
	})
	sc.Step(`^"/__health" and "/__ready" answer 200 without a credential and without an org$`, func() error {
		for _, p := range []string{"/__health", "/__ready"} {
			rep, err := w.send(newRequest("GET", p))
			if err != nil {
				return err
			}
			if rep.status != http.StatusOK {
				return fmt.Errorf("%s gave %d", p, rep.status)
			}
		}
		return nil
	})
	sc.Step(`^"/__metrics" is not served without a credential \(see metrics_and_health_exposure\.feature\)$`, func() error {
		rep, err := w.send(newRequest("GET", "/__metrics"))
		if err != nil {
			return err
		}
		if rep.status != http.StatusUnauthorized {
			return fmt.Errorf("/__metrics gave %d without a credential, want 401", rep.status)
		}
		return nil
	})
	sc.Step(`^the response does not say whether other handles exist$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for _, o := range m.orgs {
			if strings.Contains(string(w.last.body), o.handle) {
				return fmt.Errorf("the response names the handle %q: %s", o.handle, truncate(w.last.body))
			}
		}
		return nil
	})
	sc.Step(`^the management app commits the org "([^"]*)" with handle "([^"]*)" and a mapping for "([A-Z]+) ([^"]*)"$`, func(name, handle, method, url string) error {
		if err := w.addOrg(name, handle, "Team", 0, 0); err != nil {
			return err
		}
		return w.putGlobalMapping(name, "from-"+name, method, url, name, nil)
	})
	sc.Step(`^no restart, DNS change or certificate was needed$`, func() error { return nil })

	// Platform orgs and handle changes.
	sc.Step(`^the platform org "([^"]*)" with slug "([^"]*)" enables Shadow Mock$`, func(id, slug string) error {
		return w.addOrg(id, slug, "Team", 0, 0)
	})
	sc.Step(`^the platform org "([^"]*)" is renamed to slug "([^"]*)"$`, func(string, string) error { return nil })
	changeHandle := func(id, handle string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		o, ok := w.app.Directory().ByID(tenant.ID(id))
		if !ok {
			return fmt.Errorf("no org %q", id)
		}
		err = w.app.Directory().Put(tenant.Org{ID: o.ID, Handle: handle, Limits: o.Limits})
		if err == nil {
			m.orgs[id].handle = handle
		}
		return w.directoryWrite(err)
	}
	sc.Step(`^the org admin changes the handle of "([^"]*)" to "([^"]*)"$`, changeHandle)
	sc.Step(`^the org admin changed the handle of "([^"]*)" to "([^"]*)"$`, changeHandle)
	sc.Step(`^another org tries to take the handle "([^"]*)"$`, func(handle string) error {
		return w.directoryWrite(w.app.Directory().Put(tenant.Org{ID: "other-org", Handle: handle}))
	})
	sc.Step(`^an org admin tries to set the handle to "([^"]*)"$`, func(handle string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.directoryWrite(w.app.Directory().Put(tenant.Org{ID: tenant.ID(m.defaultOrg), Handle: handle}))
	})
	sc.Step(`^org "([^"]*)" has a URL-based shadow mock "([^"]*)"$`, func(org, name string) error {
		return w.record(func() error {
			rep, err := w.managedAdminCall("POST", "/sessions/url-based", "{}", org, w.setupCred(org), nil)
			if err != nil {
				return err
			}
			return w.noteSession(name, rep)
		})
	})
	sc.Step(`^org "([^"]*)" has a key-based shadow mock "([^"]*)" with the mapping:$`, func(org, name string, doc *godog.DocString) error {
		return w.record(func() error {
			rep, err := w.managedAdminCall("POST", "/sessions/key-based", "{}", org, w.setupCred(org), nil)
			if err != nil {
				return err
			}
			if err = w.noteSession(name, rep); err != nil {
				return err
			}
			rep, err = w.managedAdminCall("POST", "/sessions/"+w.vars[name+".id"]+"/mappings", doc.Content, org, w.setupCred(org), nil)
			if err != nil {
				return err
			}
			if rep.status != http.StatusCreated {
				return fmt.Errorf("adding the mapping gave %d: %s", rep.status, truncate(rep.body))
			}
			return nil
		})
	})
	sc.Step(`^the org "([^"]*)" has handle "([^"]*)"$`, func(id, handle string) error {
		if o, ok := w.app.Directory().ByID(tenant.ID(id)); !ok || o.Handle != handle {
			return fmt.Errorf("org %s is %+v, want the handle %s", id, o, handle)
		}
		return nil
	})
	sc.Step(`^within (\d+) s a "([A-Z]+)" request to "([^"]*)" returns status (\d+)$`, func(secs int, method, path string, status int) error {
		return w.eventually(time.Duration(secs)*time.Second, func() error {
			rep, err := w.send(newRequest(method, path))
			if err != nil {
				return err
			}
			if rep.status != status {
				return fmt.Errorf("%s %s gave %d, want %d", method, path, rep.status, status)
			}
			return nil
		})
	})
}

func (w *world) writeIs(code string) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	if got := writeCode(m.writeErr); got != code {
		return fmt.Errorf("the write was %s, want %s", got, code)
	}
	return nil
}

// meterRequests records n served requests against an org this month, as the mock traffic would.
func (w *world) meterRequests(org string, n int) {
	for range n {
		w.app.Usage().Request(tenant.ID(org))
	}
}

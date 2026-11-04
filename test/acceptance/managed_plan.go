package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/pwgith/shadow-mock/api"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// What the management app writes into an org's record, the plan's limits and the org's own settings, and what the
// engine then does with it (FR-11.11d, FR-11.12). The management app is played by writes to the directory.

// createSessionFor creates a key-based shadow mock of the org with the harness's own credential and leaves the
// scenario's last response as it was. It returns the reply.
func (w *world) createSessionFor(org string) (*reply, error) {
	var rep *reply
	err := w.keepLast(func() (err error) {
		rep, err = w.managedAdminCall("POST", "/sessions/key-based", "{}", org, w.setupCred(org), nil)
		return err
	})
	return rep, err
}

// activeSessions makes n key-based shadow mocks of the org that are active.
func (w *world) activeSessions(org string, n int) error {
	w.freeze()
	for range n {
		rep, err := w.createSessionFor(org)
		if err != nil {
			return err
		}
		if rep.status != http.StatusCreated {
			return fmt.Errorf("creating a shadow mock gave %d: %s", rep.status, truncate(rep.body))
		}
	}
	w.vars["sessions.active"] = strconv.Itoa(n)
	return nil
}

// createAnother creates a shadow mock the way a scenario's own call would: the answer becomes the last response.
func (w *world) createAnother() error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	_, err = w.managedAdminCall("POST", "/sessions/key-based", "{}", m.defaultOrg, w.setupCred(m.defaultOrg), nil)
	return err
}

// orgSessions lists the org's shadow mocks as the management app would see them.
func (w *world) orgSessions(org string) ([]map[string]any, error) {
	var out []map[string]any
	err := w.keepLast(func() error {
		rep, err := w.managedAdminCall("GET", "/sessions", "", org, w.setupCred(org), nil)
		if err != nil {
			return err
		}
		var doc struct {
			Sessions []map[string]any `json:"sessions"`
		}
		if rep.status != http.StatusOK || json.Unmarshal(rep.body, &doc) != nil {
			return fmt.Errorf("GET /sessions gave %d: %s", rep.status, truncate(rep.body))
		}
		out = doc.Sessions
		return nil
	})
	return out, err
}

// journalledAfter sends a request and reports whether the journal gained an entry for it.
func (w *world) journalledAfter(org string, send func() error) (bool, error) {
	before, err := w.journalOf(org)
	if err != nil {
		return false, err
	}
	if sendErr := send(); sendErr != nil {
		return false, sendErr
	}
	after, err := w.journalOf(org)
	return len(after) > len(before), err
}

// newestEntry is the org's newest journal entry.
func (w *world) newestEntry(org string) (map[string]any, error) {
	entries, err := w.journalOf(org)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("the journal is empty")
	}
	return entries[0], nil
}

// entryHasNoBody checks that a journal entry keeps no body and says that the org's settings are why.
func entryHasNoBody(e map[string]any) error {
	req, _ := e["request"].(map[string]any)          //nolint:errcheck // a missing request reads as no body
	if body, _ := req["body"].(string); body != "" { //nolint:errcheck // see above
		return fmt.Errorf("the entry keeps the request body %q", body)
	}
	bodies, _ := e["bodies"].(map[string]any)                             //nolint:errcheck // a missing note reads as none
	if reason, _ := bodies["reason"].(string); reason != "org_settings" { //nolint:errcheck // see above
		return fmt.Errorf("the entry says %v about its bodies, want the reason org_settings", bodies)
	}
	return nil
}

func (w *world) sendProbe(method, path, body string) error {
	r := newRequest(method, w.mockPath(path))
	if body != "" {
		r.body = []byte(body)
		r.header.Set("Content-Type", "text/plain")
	}
	return w.sendOne(r)
}

func (w *world) initManagedPlan(sc *godog.ScenarioContext) {
	w.initPlanLimits(sc)
	w.initOrgSettings(sc)
	w.initPlanFeatures(sc)
	sc.Step(`^the OpenAPI description and "schema/stub-mapping\.schema\.json" of that release agree with it$`, w.contractFilesAgree)
}

// contractFilesAgree checks that the mapping schema version the engine reports is the one of the OpenAPI description
// it embeds and of the schema file the release publishes (FR-11.11e). The api package's tests check that the schema
// file is the OpenAPI description's own StubMapping.
func (w *world) contractFilesAgree() error {
	var reported struct {
		MappingSchema int `json:"mappingSchema"`
	}
	if w.last == nil || json.Unmarshal(w.last.body, &reported) != nil || reported.MappingSchema == 0 {
		return fmt.Errorf("the last response does not report a mappingSchema")
	}
	var described struct {
		Info map[string]any `yaml:"info"`
	}
	if err := yaml.Unmarshal(api.OpenAPI, &described); err != nil {
		return err
	}
	raw, err := os.ReadFile("../../schema/stub-mapping.schema.json")
	if err != nil {
		return err
	}
	var published map[string]any
	if err := json.Unmarshal(raw, &published); err != nil {
		return err
	}
	// The schema's version is a JSON number, and the description's a YAML integer.
	describedVersion, _ := described.Info["x-mapping-schema-version"].(int) //nolint:errcheck // a missing version reads as 0
	publishedVersion, _ := published["x-schemaVersion"].(float64)           //nolint:errcheck // see above
	if describedVersion != reported.MappingSchema || int(publishedVersion) != reported.MappingSchema {
		return fmt.Errorf("the engine reports mappingSchema %d, the OpenAPI description carries %d and the schema file %d",
			reported.MappingSchema, describedVersion, int(publishedVersion))
	}
	return nil
}

// The shadow mocks a plan allows.
func (w *world) initPlanLimits(sc *godog.ScenarioContext) {
	sc.Step(`^org "([^"]*)" has (\d+) active sessions$`, w.activeSessions)
	sc.Step(`^(\d+) shadow mocks are active$`, func(n int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.activeSessions(m.defaultOrg, n)
	})
	sc.Step(`^the plan allows (\d+) concurrent shadow mocks and (\d+) requests per second$`, func(n, rps int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.limitOrg(m.defaultOrg, func(l *tenant.Limits) {
			l.MaxSessions, l.RequestsPerSecond, l.Burst = n, float64(rps), rps
		})
	})
	sc.Step(`^the plan allows (\d+) concurrent shadow mocks and (\d+) are active$`, func(limit, active int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if err := w.limitOrg(m.defaultOrg, func(l *tenant.Limits) { l.MaxSessions, l.IncludedSessions = limit, limit }); err != nil {
			return err
		}
		return w.activeSessions(m.defaultOrg, active)
	})
	sc.Step(`^the management app writes a plan allowing (\d+) shadow mocks and (\d+) requests per second$`, func(n, rps int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.limitOrg(m.defaultOrg, func(l *tenant.Limits) {
			l.MaxSessions, l.IncludedSessions, l.RequestsPerSecond, l.Burst = n, n, float64(rps), rps
		})
	})
	sc.Step(`^the management app writes a plan allowing (\d+) shadow mocks and (\d+) mappings per shadow mock$`, func(n, per int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.limitOrg(m.defaultOrg, func(l *tenant.Limits) {
			l.MaxSessions, l.IncludedSessions, l.MaxMappingsPerSession = n, n, per
		})
	})
	sc.Step(`^I create (?:another|a key-based) shadow mock$`, w.createAnother)
	sc.Step(`^within (\d+) seconds? another shadow mock can be created$`, func(secs int) error {
		return w.eventually(time.Duration(secs)*time.Second, func() error {
			rep, err := w.createSessionFor(w.m.defaultOrg)
			if err != nil {
				return err
			}
			if rep.status != http.StatusCreated {
				return fmt.Errorf("creating a shadow mock gave %d: %s", rep.status, truncate(rep.body))
			}
			return nil
		})
	})
	sc.Step(`^creating another shadow mock is refused$`, func() error {
		rep, err := w.createSessionFor(w.m.defaultOrg)
		if err != nil {
			return err
		}
		if rep.status != http.StatusTooManyRequests {
			return fmt.Errorf("creating a shadow mock gave %d, want 429: %s", rep.status, truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^the existing shadow mocks are unaffected$`, func() error {
		got, err := w.orgSessions(w.m.defaultOrg)
		if err != nil {
			return err
		}
		if want := w.vars["sessions.active"]; strconv.Itoa(len(got)) != want {
			return fmt.Errorf("the org has %d shadow mocks, want the %s it had", len(got), want)
		}
		return nil
	})
	sc.Step(`^org "([^"]*)" has (\d+) shadow mocks, (\d+) mappings and (\d+) files$`, w.stockOrg)
	sc.Step(`^the (\d+) shadow mocks, (\d+) mappings and (\d+) files are all still there and still served$`, w.stockIsKept)
}

// stockOrg gives an org shadow mocks that share the mappings, and the files, between them.
func (w *world) stockOrg(org string, sessions, mappings, files int) error {
	w.freeze()
	for i := range sessions {
		rep, err := w.createSessionFor(org)
		if err != nil {
			return err
		}
		var s struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		}
		if rep.status != http.StatusCreated || json.Unmarshal(rep.body, &s) != nil {
			return fmt.Errorf("creating a shadow mock gave %d: %s", rep.status, truncate(rep.body))
		}
		w.vars["stock."+strconv.Itoa(i)+".key"] = s.Key
		if err := w.keepLast(func() error { return w.fillSession(org, s.ID, i, sessions, mappings, files) }); err != nil {
			return err
		}
	}
	w.vars["sessions.active"] = strconv.Itoa(sessions)
	return nil
}

// fillSession gives session number i of n its share of the mappings and files.
func (w *world) fillSession(org, id string, i, n, mappings, files int) error {
	call := func(method, path, body string) error {
		rep, err := w.managedAdminCall(method, path, body, org, w.setupCred(org), nil)
		if err != nil {
			return err
		}
		if rep.status >= http.StatusMultipleChoices {
			return fmt.Errorf("%s %s gave %d: %s", method, path, rep.status, truncate(rep.body))
		}
		return nil
	}
	for k := range share(mappings, n, i) {
		body := fmt.Sprintf(`{"request":{"url":"/stock-%d-%d"},"response":{"status":200,"body":"ok"}}`, i, k)
		if err := call("POST", "/sessions/"+id+"/mappings", body); err != nil {
			return err
		}
	}
	for k := range share(files, n, i) {
		if err := call("PUT", fmt.Sprintf("/sessions/%s/files/stock-%d-%d.txt", id, i, k), "content"); err != nil {
			return err
		}
	}
	return nil
}

// share is how many of total go to part i of n: the first parts take the remainder.
func share(total, n, i int) int {
	s := total / n
	if i < total%n {
		s++
	}
	return s
}

// stockIsKept checks that the shadow mocks, mappings and files are all there, and that a mapping is still served.
func (w *world) stockIsKept(sessions, mappings, files int) error {
	org := w.m.defaultOrg
	list, err := w.orgSessions(org)
	if err != nil {
		return err
	}
	if len(list) != sessions {
		return fmt.Errorf("the org has %d shadow mocks, want %d", len(list), sessions)
	}
	var gotMappings, gotFiles int
	for _, s := range list {
		id, _ := s["id"].(string) //nolint:errcheck // a missing id fails the calls below
		n, f, countErr := w.sessionStock(org, id)
		if countErr != nil {
			return countErr
		}
		gotMappings, gotFiles = gotMappings+n, gotFiles+f
	}
	if gotMappings != mappings || gotFiles != files {
		return fmt.Errorf("the org has %d mappings and %d files, want %d and %d", gotMappings, gotFiles, mappings, files)
	}
	r := newRequest("GET", w.mockPath("/stock-0-0"))
	r.header.Set("X-Shadow-Mock-Key", w.vars["stock.0.key"])
	if err := w.sendOne(r); err != nil {
		return err
	}
	if w.last.status != http.StatusOK {
		return fmt.Errorf("a mapping that was there before the plan changed gave %d", w.last.status)
	}
	return nil
}

// sessionStock counts the mappings and files of a shadow mock.
func (w *world) sessionStock(org, id string) (mappings, files int, err error) {
	err = w.keepLast(func() error {
		rep, callErr := w.managedAdminCall("GET", "/sessions/"+id+"/mappings", "", org, w.setupCred(org), nil)
		if callErr != nil {
			return callErr
		}
		if mappings, callErr = listLen(rep.body, "$.mappings"); callErr != nil {
			return callErr
		}
		rep, callErr = w.managedAdminCall("GET", "/sessions/"+id+"/files", "", org, w.setupCred(org), nil)
		if callErr != nil {
			return callErr
		}
		var names []string
		if callErr = json.Unmarshal(rep.body, &names); callErr != nil {
			return fmt.Errorf("the file list is not a list of names: %s", truncate(rep.body))
		}
		files = len(names)
		return nil
	})
	return mappings, files, err
}

// What an org chooses for its journal and its responses.
func (w *world) initOrgSettings(sc *godog.ScenarioContext) {
	set := func(change func(*tenant.Settings)) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.updateOrg(m.defaultOrg, func(o *tenant.Org) { change(&o.Settings) })
	}
	sc.Step(`^the org "([^"]*)" has setting "gzip" set to "off"$`, func(org string) error {
		return w.updateOrg(org, func(o *tenant.Org) { o.Settings.NoCompression = true })
	})
	sc.Step(`^the management app (?:changes|turns) response compression (?:to )?off(?: for the org)?$`, func() error {
		return set(func(s *tenant.Settings) { s.NoCompression = true })
	})
	sc.Step(`^the management app changes journal capture to "([^"]*)"$`, func(capture string) error {
		return set(func(s *tenant.Settings) { s.JournalCapture = capture })
	})
	sc.Step(`^the management app sets the org's journal capture to "([^"]*)" and body storage off$`, func(capture string) error {
		return set(func(s *tenant.Settings) { s.JournalCapture, s.NoJournalBodies = capture, true })
	})
	sc.Step(`^the org stores no journal bodies$`, func() error {
		return set(func(s *tenant.Settings) { s.NoJournalBodies = true })
	})
	sc.Step(`^the org stores journal bodies$`, func() error {
		return set(func(s *tenant.Settings) { s.NoJournalBodies = false })
	})
	sc.Step(`^the SUT has sent a request with a body$`, func() error { return w.sendProbe("POST", "/probe", "payload") })
	sc.Step(`^the SUT has sent a request with a (\d+) KiB body$`, func(kib int) error {
		return w.sendProbe("POST", "/probe", string(make([]byte, kib<<10)))
	})
	sc.Step(`^the response says bodies are not stored because of the org's journal settings$`, func() error {
		return w.bodiesNote(false, "org_settings", 0)
	})
	sc.Step(`^the response says the body was truncated at (\d+) KiB$`, func(kib int) error {
		return w.bodiesNote(true, "size_cap", kib<<10)
	})

	sc.Step(`^within (\d+) seconds? a (?:served|matched global) request is not journalled$`, func(secs int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if err := w.putGlobalMapping(m.defaultOrg, "journal-probe", "GET", "/journal-probe", "ok"); err != nil {
			return err
		}
		return w.eventually(time.Duration(secs)*time.Second, func() error {
			added, jErr := w.journalledAfter(m.defaultOrg, func() error { return w.sendProbe("GET", "/journal-probe", "") })
			if jErr != nil {
				return jErr
			}
			if w.last.status != http.StatusOK {
				return fmt.Errorf("the request gave %d, want it served: %s", w.last.status, truncate(w.last.body))
			}
			if added {
				return fmt.Errorf("the request was journalled")
			}
			return nil
		})
	})
	notCompressed := func(secs int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if err := w.putGlobalMapping(m.defaultOrg, "compress-probe", "GET", "/compress-probe", "a body to compress"); err != nil {
			return err
		}
		return w.eventually(time.Duration(secs)*time.Second, func() error {
			r := newRequest("GET", w.mockPath("/compress-probe"))
			r.header.Set("Accept-Encoding", "gzip")
			if err := w.sendOne(r); err != nil {
				return err
			}
			if w.last.status != http.StatusOK {
				return fmt.Errorf("the request gave %d, want it served: %s", w.last.status, truncate(w.last.body))
			}
			if enc := w.last.header.Get("Content-Encoding"); enc != "" {
				return fmt.Errorf("the response has Content-Encoding %q", enc)
			}
			return nil
		})
	}
	sc.Step(`^within (\d+) seconds? a response to a client that accepts gzip is not compressed$`, notCompressed)
	sc.Step(`^a response to a client that accepts gzip is not compressed$`, func() error { return notCompressed(1) })
	sc.Step(`^an unmatched request is journalled without a body$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if err := w.sendProbe("POST", "/nothing-serves-this", "secret"); err != nil {
			return err
		}
		return w.newestEntryHasNoBody(m.defaultOrg)
	})
	sc.Step(`^a request to a shadow mock is journalled without a body$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		rep, err := w.createSessionFor(m.defaultOrg)
		if err != nil {
			return err
		}
		var s struct {
			Key string `json:"key"`
		}
		if rep.status != http.StatusCreated || json.Unmarshal(rep.body, &s) != nil {
			return fmt.Errorf("creating a shadow mock gave %d: %s", rep.status, truncate(rep.body))
		}
		r := newRequest("POST", w.mockPath("/to-a-shadow-mock"))
		r.body = []byte("secret")
		r.header.Set("X-Shadow-Mock-Key", s.Key)
		if err := w.sendOne(r); err != nil {
			return err
		}
		return w.newestEntryHasNoBody(m.defaultOrg)
	})
}

func (w *world) newestEntryHasNoBody(org string) error {
	e, err := w.newestEntry(org)
	if err != nil {
		return err
	}
	return entryHasNoBody(e)
}

// bodiesNote checks the note an opened journal entry carries about its bodies.
func (w *world) bodiesNote(stored bool, reason string, truncatedAt int) error {
	var doc struct {
		Bodies struct {
			Stored      bool   `json:"stored"`
			Reason      string `json:"reason"`
			TruncatedAt int    `json:"truncatedAt"`
		} `json:"bodies"`
	}
	if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil {
		return fmt.Errorf("the last response is not a journal entry")
	}
	b := doc.Bodies
	if b.Stored != stored || b.Reason != reason || b.TruncatedAt != truncatedAt {
		return fmt.Errorf("the entry's note on its bodies is %+v, want stored %v, reason %q, truncated at %d", b, stored, reason, truncatedAt)
	}
	return nil
}

// A feature the plan lacks: the org's settings for it stay, and do not apply.
func (w *world) initPlanFeatures(sc *godog.ScenarioContext) {
	from := func(ip string) error {
		r := newRequest("GET", w.mockPath("/hello"))
		r.header.Set("X-Forwarded-For", ip)
		return w.sendOne(r)
	}
	plan := func(without bool) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.limitOrg(m.defaultOrg, func(l *tenant.Limits) {
			if without {
				l.Without |= tenant.FeatureIPAllowlist
			} else {
				l.Without &^= tenant.FeatureIPAllowlist
			}
		})
	}
	sc.Step(`^the management app writes a plan without the IP allowlist$`, func() error { return plan(true) })
	sc.Step(`^the management app writes a plan with the allowlist again$`, func() error { return plan(false) })
	sc.Step(`^the allowlist is not applied$`, func() error {
		if err := from("203.0.113.4"); err != nil {
			return err
		}
		if w.last.status != http.StatusOK {
			return fmt.Errorf("a request from outside the allowlist gave %d, want it served", w.last.status)
		}
		return nil
	})
	sc.Step(`^the entries are still stored$`, func() error {
		o, ok := w.app.Directory().ByID(tenant.ID(w.m.defaultOrg))
		if !ok || len(o.Allowlist) == 0 {
			return fmt.Errorf("the org's allowlist is gone: %+v", o)
		}
		return nil
	})
	sc.Step(`^the allowlist applies again without being entered again$`, func() error {
		return w.eventually(time.Second, func() error {
			if err := from("203.0.113.4"); err != nil {
				return err
			}
			if w.last.status != http.StatusForbidden {
				return fmt.Errorf("a request from outside the allowlist gave %d, want 403", w.last.status)
			}
			return nil
		})
	})
}

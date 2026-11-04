package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// What the management app needs from the admin API beyond WireMock's (FR-11.11): the session list and journal
// search, stats, counters, promoting a shadow mock, dry runs and the contract.

// storyStart is where the scenarios that need hours of history begin.
var storyStart = time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)

// tableRows turns a table into one map per row, keyed by the header cells.
func tableRows(t *godog.Table) []map[string]string {
	out := make([]map[string]string, 0, len(t.Rows)-1)
	for _, row := range t.Rows[1:] {
		r := map[string]string{}
		for i, c := range row.Cells {
			r[t.Rows[0].Cells[i].Value] = c.Value
		}
		out = append(out, r)
	}
	return out
}

// jsonStrings reads the string at a field of each item of an array in the last response.
func (w *world) jsonStrings(array string, pick func(map[string]any) string) ([]string, error) {
	var doc map[string]any
	if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil {
		return nil, fmt.Errorf("the last response is not JSON: %+v", w.last)
	}
	items, _ := doc[array].([]any) //nolint:errcheck // a missing array reads as empty
	out := make([]string, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any) //nolint:errcheck // an item that is not an object reads as empty
		out = append(out, pick(m))
	}
	return out, nil
}

// sessionIDs replaces the name of a session in a path with its id.
func (w *world) withSessionIDs(p string) string {
	for k, v := range w.vars {
		if name, ok := strings.CutSuffix(k, ".id"); ok {
			p = strings.ReplaceAll(p, "/sessions/"+name+"/", "/sessions/"+v+"/")
		}
	}
	return p
}

// newSession makes a key-based shadow mock named name with the admin API, as the harness's own caller.
func (w *world) newSession(name string) error { return w.createSession("key-based", name, "{}") }

// putSessionMapping stores a mapping in a shadow mock.
func (w *world) putSessionMapping(name, doc string) error {
	_, err := w.mustAdmin("POST", "/sessions/${"+name+".id}/mappings", doc, 201)
	return err
}

const counterStub = "eeeeeeee-0000-0000-0000-000000000101"

func (w *world) initManagedAdminAPI(sc *godog.ScenarioContext) {
	// Shadow mocks to list. The expired ones are made first and the clock moves past their life.
	sc.Step(`^shadow mocks:$`, func(t *godog.Table) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		rows := tableRows(t)
		create := func(r map[string]string, ttl *int64) error {
			kind := session.Kind(r["kind"])
			_, createErr := w.app.Service().Sessions.Create(ctxBackground(), tenant.ID(m.defaultOrg), session.CreateRequest{
				Kind: kind, Name: r["name"], CreatedBy: r["createdBy"], CreatedByKey: r["createdByKey"], TTLSeconds: ttl,
			})
			return createErr
		}
		ttl := int64(60)
		for _, r := range rows {
			if r["state"] == "expired" {
				if err := create(r, &ttl); err != nil {
					return err
				}
			}
		}
		w.clk.advance(2 * time.Minute)
		for _, r := range rows {
			if r["state"] != "expired" {
				if err := create(r, nil); err != nil {
					return err
				}
			}
		}
		return nil
	})
	sc.Step(`^the sessions listed are "([^"]*)"$`, func(want string) error {
		got, err := w.jsonStrings("sessions", func(s map[string]any) string { n, _ := s["name"].(string); return n }) //nolint:errcheck // a missing name reads as empty
		if err != nil {
			return err
		}
		exp := strings.Split(want, ", ")
		slices.Sort(got)
		slices.Sort(exp)
		if !slices.Equal(got, exp) {
			return fmt.Errorf("the sessions listed are %v, want %v", got, exp)
		}
		return nil
	})
	sc.Step(`^(\d+) shadow mocks made one second apart$`, func(n int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		for i := range n {
			if _, err = w.app.Service().Sessions.Create(ctxBackground(), tenant.ID(m.defaultOrg), session.CreateRequest{
				Kind: session.KeyBased, Name: "m" + strconv.Itoa(i), CreatedBy: "erin",
			}); err != nil {
				return err
			}
			w.clk.advance(time.Second)
		}
		return nil
	})
	sc.Step(`^(\d+) sessions are listed, newest first$`, func(n int) error {
		var doc struct {
			Sessions []struct {
				ID        string    `json:"id"`
				CreatedAt time.Time `json:"createdAt"`
			} `json:"sessions"`
		}
		if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil {
			return fmt.Errorf("the last response is not a session list: %+v", w.last)
		}
		if len(doc.Sessions) != n {
			return fmt.Errorf("%d sessions are listed, want %d", len(doc.Sessions), n)
		}
		for i := 1; i < len(doc.Sessions); i++ {
			if doc.Sessions[i].CreatedAt.After(doc.Sessions[i-1].CreatedAt) {
				return fmt.Errorf("session %d is newer than the one before it", i)
			}
		}
		w.vars["page.first"], w.vars["page.last"] = doc.Sessions[0].ID, doc.Sessions[len(doc.Sessions)-1].ID
		return nil
	})
	sc.Step(`^the response has a cursor for the next page$`, func() error {
		v, found, err := w.jsonOne("$.meta.nextCursor")
		if err != nil || !found || text(v) == "" {
			return fmt.Errorf("the response has no cursor: %v %v", found, err)
		}
		w.vars["cursor"] = text(v)
		return nil
	})
	sc.Step(`^following the cursor gives the next (\d+)$`, func(n int) error {
		if err := w.callWith("GET", "/sessions?limit="+strconv.Itoa(n)+"&cursor="+w.vars["cursor"], "", nil); err != nil {
			return err
		}
		prevLast := w.vars["page.last"]
		var doc struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		if json.Unmarshal(w.last.body, &doc) != nil || len(doc.Sessions) != n {
			return fmt.Errorf("the next page is %s, want %d sessions", truncate(w.last.body), n)
		}
		for _, s := range doc.Sessions {
			if s.ID == prevLast || s.ID == w.vars["page.first"] {
				return fmt.Errorf("the next page repeats session %s", s.ID)
			}
		}
		return nil
	})

	// Journal entries to search, written straight into the journal.
	appendEntry := func(m *managedWorld, i int, at time.Time, method, p string, status int, layer, sess string, matched bool) {
		e := &journal.Entry{
			ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), LoggedAt: at, Tenant: tenant.ID(m.defaultOrg), Layer: layer,
			Request:  &match.Request{Method: method, Path: p, Header: http.Header{}, Query: url.Values{}},
			Response: journal.Response{Status: status},
		}
		if sess != "" {
			e.SessionID, e.Sessions = sess, []string{sess}
		}
		if matched {
			e.Stub = &model.StubMapping{ID: "00000000-0000-4000-8000-000000000001"}
		}
		w.app.Service().Journal.Append(e)
	}
	sc.Step(`^journal entries:$`, func(t *godog.Table) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for i, r := range tableRows(t) {
			status, _ := strconv.Atoi(r["status"]) //nolint:errcheck // the table has numbers
			appendEntry(m, i+1, storyStart.Add(time.Duration(i)*time.Second), r["method"], r["path"], status, r["layer"], r["session"], r["matched"] == "true")
		}
		return nil
	})
	sc.Step(`^the entries listed are "([^"]*)"$`, func(want string) error {
		got, err := w.jsonStrings("requests", func(e map[string]any) string {
			req, _ := e["request"].(map[string]any) //nolint:errcheck // a missing request reads as empty
			u, _ := req["url"].(string)             //nolint:errcheck // see above
			return u
		})
		if err != nil {
			return err
		}
		if have := strings.Join(got, ", "); have != want {
			return fmt.Errorf("the entries listed are %q, want %q", have, want)
		}
		return nil
	})
	sc.Step(`^(\d+) journal entries over (\d+) hours$`, func(n, hours int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		step := time.Duration(hours) * time.Hour / time.Duration(n)
		for i := range n {
			appendEntry(m, i+1, storyStart.Add(time.Duration(i)*step), "GET", "/claims/"+strconv.Itoa(i), 200, "global", "", true)
		}
		w.vars["hour2"] = storyStart.Add(time.Hour).Format(time.RFC3339)
		return nil
	})
	sc.Step(`^only entries from that hour are listed, newest first$`, func() error {
		var doc struct {
			Requests []struct {
				Request struct {
					LoggedDate int64 `json:"loggedDate"`
				} `json:"request"`
			} `json:"requests"`
		}
		if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil || len(doc.Requests) == 0 {
			return fmt.Errorf("the last response is not a journal list: %s", truncate(w.last.body))
		}
		from, _ := time.Parse(time.RFC3339, w.vars["hour2"]) //nolint:errcheck // set by the step that made it
		for i, e := range doc.Requests {
			at := time.UnixMilli(e.Request.LoggedDate)
			if at.Before(from) {
				return fmt.Errorf("entry %d is from %s, before %s", i, at, from)
			}
			if i > 0 && at.After(time.UnixMilli(doc.Requests[i-1].Request.LoggedDate)) {
				return fmt.Errorf("entry %d is newer than the one before it", i)
			}
		}
		return nil
	})

	// Which set owns a mapping.
	sc.Step(`^a mapping "([^"]*)" with metadata source "([^"]*)" in the global layer$`, func(name, src string) error {
		return w.writeLayerMapping(layerMapping{name: name, method: "GET", url: "/" + name, metadata: source(src)})
	})
	sc.Step(`^a mapping "([^"]*)" with no metadata in the global layer$`, func(name string) error {
		return w.writeLayerMapping(layerMapping{name: name, method: "GET", url: "/" + name})
	})
	sc.Step(`^the SUT sends a request that "([^"]*)" serves$`, func(name string) error {
		return w.served("GET", "/acme/"+name)
	})
	sc.Step(`^the SUT sends a request that nearly matches "([^"]*)"$`, func(name string) error {
		return w.sendOne(newRequest("POST", "/acme/"+name))
	})
	sc.Step(`^I open the journal entry$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		var doc struct {
			Requests []struct {
				ID string `json:"id"`
			} `json:"requests"`
		}
		rep, err := w.managedAdminCall("GET", "/requests", "", "", w.setupCred(m.defaultOrg), nil)
		if err != nil {
			return err
		}
		if json.Unmarshal(rep.body, &doc) != nil || len(doc.Requests) == 0 {
			return fmt.Errorf("the journal is empty: %s", truncate(rep.body))
		}
		_, err = w.managedAdminCall("GET", "/requests/"+doc.Requests[0].ID, "", "", w.setupCred(m.defaultOrg), nil)
		return err
	})
	sc.Step(`^I open the near-miss analysis$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		_, err = w.managedAdminCall("GET", "/requests/unmatched/near-misses", "", "", w.setupCred(m.defaultOrg), nil)
		return err
	})
	sc.Step(`^the entry's "([^"]*)" is "([^"]*)"$`, func(field, want string) error {
		v, found, err := w.jsonOne("$." + field)
		if err != nil || !found || text(v) != want {
			return fmt.Errorf("the entry's %s is %v (found %v, %v), want %q", field, v, found, err, want)
		}
		return nil
	})

	// Stats over hours of traffic.
	sc.Step(`^the org has served traffic for (\d+) hours$`, func(hours int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		w.clk.set(storyStart)
		ttl := int64(60)
		if _, err = w.app.Service().Sessions.Create(ctxBackground(), tenant.ID(m.defaultOrg), session.CreateRequest{Kind: session.KeyBased, Name: "short", TTLSeconds: &ttl}); err != nil {
			return err
		}
		if err := w.putGlobalMapping(m.defaultOrg, "hello", "GET", "/hello", "hello"); err != nil {
			return err
		}
		for h := range hours {
			w.clk.set(storyStart.Add(time.Duration(h) * time.Hour))
			if h == 1 {
				w.app.Service().ReapOnce(ctxBackground())
			}
			for _, p := range []string{"/hello", "/hello", "/unknown"} {
				if err := w.sendOne(newRequest("GET", "/"+m.orgs[m.defaultOrg].handle+p)); err != nil {
					return err
				}
			}
		}
		w.clk.set(storyStart.Add(time.Duration(hours-1)*time.Hour + 30*time.Minute))
		if err := w.sendOne(newRequest("GET", "/"+m.orgs[m.defaultOrg].handle+"/hello")); err != nil {
			return err
		}
		w.vars["from"], w.vars["to"] = storyStart.Format(time.RFC3339), storyStart.Add(time.Duration(hours)*time.Hour).Format(time.RFC3339)
		return nil
	})
	sc.Step(`^there is a point for each hour with requests by layer, match rate, top unmatched paths, latency p50 and p99, detection stage hits, active sessions and expiries$`, func() error {
		var doc struct {
			Points []map[string]any `json:"points"`
		}
		if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil {
			return fmt.Errorf("the last response is not stats history: %s", truncate(w.last.body))
		}
		if len(doc.Points) != 3 {
			return fmt.Errorf("the history has %d points, want 3", len(doc.Points))
		}
		for i, p := range doc.Points {
			for _, field := range []string{"requestsPerMinute", "matchRate", "topUnmatchedPaths", "latencyMs", "detectionHits", "activeSessions", "expiredSessions"} {
				if _, has := p[field]; !has {
					return fmt.Errorf("point %d has no %q: %v", i, field, p)
				}
			}
			if mr, _ := p["matchRate"].(float64); mr <= 0 { //nolint:errcheck // a missing rate reads as zero
				return fmt.Errorf("point %d has a match rate of %v, want some traffic", i, p["matchRate"])
			}
		}
		return nil
	})
	sc.Step(`^the IP rejections by source list the top (\d+)$`, func(n int) error {
		var doc struct {
			Points []struct {
				IPRejections []any `json:"ipRejections"`
			} `json:"points"`
		}
		if json.Unmarshal(w.last.body, &doc) != nil || len(doc.Points) == 0 {
			return fmt.Errorf("the last response is not stats history: %s", truncate(w.last.body))
		}
		for i, p := range doc.Points {
			if p.IPRejections == nil || len(p.IPRejections) > n {
				return fmt.Errorf("point %d lists %v rejections, want a list of at most %d", i, p.IPRejections, n)
			}
		}
		return nil
	})
	sc.Step(`^it shows the present minute$`, func() error {
		var doc struct {
			Minutes []struct {
				Minute time.Time `json:"minute"`
			} `json:"requestsPerMinute"`
		}
		if json.Unmarshal(w.last.body, &doc) != nil {
			return fmt.Errorf("the last response is not live stats: %s", truncate(w.last.body))
		}
		now := w.clk.Now().Truncate(time.Minute)
		for _, m := range doc.Minutes {
			if m.Minute.Equal(now) {
				return nil
			}
		}
		return fmt.Errorf("the live stats have no minute %s: %s", now, truncate(w.last.body))
	})

	// Counters.
	sc.Step(`^a shadow mock "([^"]*)" with call responses$`, func(name string) error {
		if err := w.newSession(name); err != nil {
			return err
		}
		return w.putSessionMapping(name, `{"id":"`+counterStub+`","name":"claims-get","request":{"method":"GET","url":"/claims/1"},"response":{"status":200},"callResponses":[`+
			`{"calls":"1","response":{"status":200,"body":"first"}},{"calls":"2","response":{"status":200,"body":"second"}},{"calls":"3","response":{"status":200,"body":"third"}}]}`)
	})
	sc.Step(`^I set the counter for "([^"]*)" in "([^"]*)" to (\d+)$`, func(_, name string, n int) error {
		_, err := w.mustAdmin("PUT", "/sessions/${"+name+".id}/counters/"+counterStub, `{"count":`+strconv.Itoa(n)+`}`, 200)
		return err
	})
	sc.Step(`^the next response is the third in the list$`, func() error {
		r := newRequest("GET", "/acme/claims/1")
		r.header.Set("X-Shadow-Mock-Key", w.vars["S1.key"])
		rep, err := w.send(r)
		if err != nil {
			return err
		}
		if rep.status != http.StatusOK || string(rep.body) != "third" {
			return fmt.Errorf("the next response is %d %q, want the third", rep.status, truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^I can read the counter and reset it$`, func() error {
		rep, err := w.mustAdmin("GET", "/sessions/${S1.id}/counters", "", 200)
		if err != nil {
			return err
		}
		if !strings.Contains(string(rep.body), counterStub) {
			return fmt.Errorf("the counters do not list the stub: %s", truncate(rep.body))
		}
		if _, err = w.mustAdmin("DELETE", "/sessions/${S1.id}/counters/"+counterStub, "", 200); err != nil {
			return err
		}
		rep, err = w.mustAdmin("GET", "/sessions/${S1.id}/counters", "", 200)
		if err != nil {
			return err
		}
		if strings.Contains(string(rep.body), `"count":3`) {
			return fmt.Errorf("the counter was not reset: %s", truncate(rep.body))
		}
		return nil
	})

	// Promoting a shadow mock.
	sc.Step(`^a shadow mock "([^"]*)" with (\d+) mappings that use (\d+) file$`, func(name string, mappings, _ int) error {
		if err := w.newSession(name); err != nil {
			return err
		}
		for i := range mappings {
			body := `"body":"inline"`
			if i == 0 {
				body = `"bodyFileName":"greeting.txt"`
			}
			if err := w.putSessionMapping(name, fmt.Sprintf(`{"request":{"method":"GET","url":"/p/%d"},"response":{"status":200,%s}}`, i, body)); err != nil {
				return err
			}
		}
		_, err := w.mustAdmin("PUT", "/sessions/${"+name+".id}/files/greeting.txt", "hello", 200, 201)
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "(/sessions/[^"]*)" and "([^"]*)" "(/sessions/[^"]*)"$`, func(m1, p1, m2, p2 string) error {
		first, err := w.mustAdmin(m1, w.withSessionIDs(p1), "", 200)
		if err != nil {
			return err
		}
		w.vars["promote.mappings"] = string(first.body)
		second, err := w.mustAdmin(m2, w.withSessionIDs(p2), "", 200)
		if err != nil {
			return err
		}
		w.vars["promote.files"] = string(second.body)
		return nil
	})
	sc.Step(`^I get both mappings and the file's content$`, func() error {
		var doc struct {
			Mappings []any `json:"mappings"`
		}
		if json.Unmarshal([]byte(w.vars["promote.mappings"]), &doc) != nil || len(doc.Mappings) != 2 {
			return fmt.Errorf("the shadow mock's mappings are %s, want 2", w.vars["promote.mappings"])
		}
		if !strings.Contains(w.vars["promote.files"], "greeting.txt") {
			return fmt.Errorf("the shadow mock's files are %s, want greeting.txt", w.vars["promote.files"])
		}
		rep, err := w.mustAdmin("GET", "/sessions/${S1.id}/files/greeting.txt", "", 200)
		if err != nil {
			return err
		}
		if string(rep.body) != "hello" {
			return fmt.Errorf("the file holds %q, want hello", truncate(rep.body))
		}
		return nil
	})

	// Dry runs.
	sc.Step(`^I dry-run "([A-Z]+) ([^"]*)" against "([^"]*)"$`, func(method, p, name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		scope := store.Global(tenant.ID(m.defaultOrg))
		w.vars["journal.before"] = strconv.Itoa(len(w.app.Service().Journal.Query(scope, nil, 0)))
		before, err := w.app.Service().Counters.List(ctxBackground(), scope)
		if err != nil {
			return err
		}
		w.vars["counters.before"] = fmt.Sprint(len(before))
		return w.callWith("POST", "/mappings/dry-run", fmt.Sprintf(`{"request":{"method":%q,"url":%q},"sessionId":%q}`, method, p, w.vars[name+".id"]), nil)
	})
	sc.Step(`^the response says the layer, the mapping and the rendered response$`, func() error {
		var doc struct {
			Matched     bool           `json:"matched"`
			Layer       string         `json:"layer"`
			StubMapping map[string]any `json:"stubMapping"`
			Response    struct {
				Status int `json:"status"`
			} `json:"response"`
		}
		if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil || !doc.Matched || doc.Layer == "" || doc.StubMapping == nil || doc.Response.Status == 0 {
			return fmt.Errorf("the dry run answered %+v", w.last)
		}
		return nil
	})
	sc.Step(`^the journal and counters are unchanged$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		scope := store.Global(tenant.ID(m.defaultOrg))
		if now := strconv.Itoa(len(w.app.Service().Journal.Query(scope, nil, 0))); now != w.vars["journal.before"] {
			return fmt.Errorf("the journal has %s entries, had %s", now, w.vars["journal.before"])
		}
		cs, err := w.app.Service().Counters.List(ctxBackground(), scope)
		if err != nil {
			return err
		}
		if now := fmt.Sprint(len(cs)); now != w.vars["counters.before"] {
			return fmt.Errorf("there are %s counters, had %s", now, w.vars["counters.before"])
		}
		return nil
	})

	// The contract.
	sc.Step(`^the response has "([^"]*)", "([^"]*)" and "([^"]*)"$`, func(a, b, c string) error {
		var doc map[string]any
		if w.last == nil || json.Unmarshal(w.last.body, &doc) != nil {
			return fmt.Errorf("the last response is not JSON: %+v", w.last)
		}
		for _, k := range []string{a, b, c} {
			if _, has := doc[k]; !has {
				return fmt.Errorf("the response has no %q: %s", k, truncate(w.last.body))
			}
		}
		return nil
	})
	sc.Step(`^I create a mapping through validate with a field the engine does not know$`, func() error {
		w.vars["unknown.doc"] = `{"request":{"method":"GET","url":"/u"},"response":{"status":200},"x-owner":{"team":"claims"}}`
		return w.callWith("POST", "/mappings/validate", w.vars["unknown.doc"], nil)
	})
	sc.Step(`^the answer is valid$`, func() error {
		v, found, err := w.jsonOne("$.valid")
		if err != nil || !found || v != true {
			return fmt.Errorf("the answer is %v (found %v, %v): %s", v, found, err, truncate(w.last.body))
		}
		return nil
	})
	sc.Step(`^storing and reading the mapping returns the field unchanged$`, func() error {
		if err := w.newSession("U1"); err != nil {
			return err
		}
		rep, err := w.mustAdmin("POST", "/sessions/${U1.id}/mappings", w.vars["unknown.doc"], 201)
		if err != nil {
			return err
		}
		var stored struct {
			ID string `json:"id"`
		}
		err = json.Unmarshal(rep.body, &stored)
		if err != nil {
			return err
		}
		rep, err = w.mustAdmin("GET", "/sessions/${U1.id}/mappings/"+stored.ID, "", 200)
		if err != nil {
			return err
		}
		var got map[string]any
		if err := json.Unmarshal(rep.body, &got); err != nil {
			return err
		}
		if team, _ := got["x-owner"].(map[string]any)["team"].(string); team != "claims" { //nolint:errcheck // a missing field reads as empty
			return fmt.Errorf("the field came back as %v, want it unchanged", got["x-owner"])
		}
		return nil
	})
}

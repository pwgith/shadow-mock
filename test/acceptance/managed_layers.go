package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

// layerMapping is a mapping the management app writes: a name, what it matches, and what the engine should keep.
type layerMapping struct {
	name, method, url string
	priority          int
	metadata          map[string]any
}

// write puts the mapping in the default org's global layer. The body of its response is the mapping's name, so a
// step can tell which mapping served a request.
func (w *world) writeLayerMapping(lm layerMapping) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	return w.record(func() error {
		h := fnv.New64a()
		_, _ = h.Write([]byte(m.defaultOrg + "/" + lm.name))
		doc := map[string]any{
			"id": fmt.Sprintf("00000000-0000-4000-8000-%012x", h.Sum64()&0xffffffffffff), "name": lm.name,
			"request":  map[string]any{"method": lm.method, "url": lm.url},
			"response": map[string]any{"status": 200, "body": lm.name},
		}
		if strings.ContainsAny(lm.url, ".*[") {
			doc["request"] = map[string]any{"method": lm.method, "urlPattern": lm.url}
		}
		if lm.priority > 0 {
			doc["priority"] = lm.priority
		}
		if lm.metadata != nil {
			doc["metadata"] = lm.metadata
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		return w.putGlobalMappingJSON(m.defaultOrg, raw)
	})
}

func source(s string) map[string]any { return map[string]any{"source": s} }

func (w *world) initManagedLayers(sc *godog.ScenarioContext) {
	sc.Step(`^the management app writes mapping "([^"]*)" for "([A-Z]+) ([^"]*)" with metadata source "([^"]*)"$`, func(name, method, url, src string) error {
		return w.writeLayerMapping(layerMapping{name: name, method: method, url: url, metadata: source(src)})
	})
	sc.Step(`^the management app writes mapping "([^"]*)" for "([A-Z]+) ([^"]*)" at priority (\d+) with metadata source "([^"]*)"$`, func(name, method, url string, prio int, src string) error {
		return w.writeLayerMapping(layerMapping{name: name, method: method, url: url, priority: prio, metadata: source(src)})
	})
	sc.Step(`^the management app writes mapping "([^"]*)" for "([A-Z]+) ([^"]*)" with no metadata$`, func(name, method, url string) error {
		return w.writeLayerMapping(layerMapping{name: name, method: method, url: url})
	})
	sc.Step(`^the management app writes mapping "([^"]*)" with metadata source "([^"]*)"$`, func(name, src string) error {
		return w.writeLayerMapping(layerMapping{name: name, method: "GET", url: "/claims/1", metadata: source(src)})
	})
	sc.Step(`^the management app writes mapping "([^"]*)" with metadata source "([^"]*)" that buries "([^"]*)"$`, func(name, src, _ string) error {
		return w.writeLayerMapping(layerMapping{name: name, method: "GET", url: "/claims/1", priority: 1, metadata: source(src)})
	})
	sc.Step(`^the engine stores and serves it$`, func() error {
		rep, err := w.send(newRequest("GET", "/acme/claims/1"))
		if err != nil {
			return err
		}
		if rep.status != http.StatusOK || string(rep.body) != "n" {
			return fmt.Errorf("GET /acme/claims/1 gave %d %q, want the new mapping n", rep.status, truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^the management app writes mapping "([^"]*)" with metadata:$`, func(name string, t *godog.Table) error {
		md := map[string]any{}
		for _, row := range t.Rows[1:] {
			var v any = row.Cells[1].Value
			if strings.HasPrefix(row.Cells[1].Value, "{") {
				var obj any
				if err := json.Unmarshal([]byte(row.Cells[1].Value), &obj); err != nil {
					return err
				}
				v = obj
			}
			md[row.Cells[0].Value] = v
		}
		w.m.lastMetadata = md
		return w.writeLayerMapping(layerMapping{name: name, method: "GET", url: "/" + name, metadata: md})
	})
	sc.Step(`^I read the mapping "([^"]*)" through the admin API$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(m.defaultOrg + "/" + name))
		_, err = w.managedAdminCall("GET", fmt.Sprintf("/mappings/00000000-0000-4000-8000-%012x", h.Sum64()&0xffffffffffff), "", "", w.setupCred(m.defaultOrg), nil)
		return err
	})
	sc.Step(`^the metadata is returned exactly as written$`, func() error {
		var got struct {
			Metadata map[string]any `json:"metadata"`
		}
		if err := json.Unmarshal(w.last.body, &got); err != nil {
			return err
		}
		want, _ := json.Marshal(w.m.lastMetadata) //nolint:errcheck // plain values
		have, _ := json.Marshal(got.Metadata)     //nolint:errcheck // plain values
		if !bytes.Equal(want, have) {
			return fmt.Errorf("metadata is %s, want %s", have, want)
		}
		return nil
	})
	sc.Step(`^it is served by "([^"]*)"$`, func(name string) error {
		if w.last.status != http.StatusOK || string(w.last.body) != name {
			return fmt.Errorf("the request gave %d %q, want it served by %q", w.last.status, truncate(w.last.body), name)
		}
		return nil
	})
	sc.Step(`^it is served by the shadow mock's mapping$`, func() error {
		if w.last.status != http.StatusOK || string(w.last.body) != "shadow" {
			return fmt.Errorf("the request gave %d %q, want the shadow mock's mapping", w.last.status, truncate(w.last.body))
		}
		return nil
	})
	sc.Step(`^"([A-Z]+) ([^"]*)" and "([A-Z]+) ([^"]*)" are both served$`, func(m1, p1, m2, p2 string) error {
		if err := w.served(m1, p1); err != nil {
			return err
		}
		return w.served(m2, p2)
	})
	sc.Step(`^mappings "([^"]*)" and "([^"]*)" with metadata source "([^"]*)" and "([^"]*)" with metadata source "([^"]*)"$`, func(a, b, srcAB, c, srcC string) error {
		for _, n := range []string{a, b} {
			if err := w.writeLayerMapping(layerMapping{name: n, method: "GET", url: "/" + n, metadata: source(srcAB)}); err != nil {
				return err
			}
		}
		return w.writeLayerMapping(layerMapping{name: c, method: "GET", url: "/" + c, metadata: source(srcC)})
	})
	sc.Step(`^the management app removes the mappings whose metadata source is "([^"]*)"$`, func(src string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.record(func() error { return w.removeBySource(m.defaultOrg, src) })
	})
	sc.Step(`^"([^"]*)" and "([^"]*)" are gone$`, func(a, b string) error {
		for _, n := range []string{a, b} {
			rep, err := w.send(newRequest("GET", "/acme/"+n))
			if err != nil {
				return err
			}
			if rep.status != http.StatusNotFound {
				return fmt.Errorf("/acme/%s gave %d, want it gone", n, rep.status)
			}
		}
		return nil
	})
	sc.Step(`^"([^"]*)" is still served$`, func(n string) error { return w.served("GET", "/acme/"+n) })
	sc.Step(`^its journal entry has no "([^"]*)"$`, func(field string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("GET", "/requests", "", "", w.setupCred(m.defaultOrg), nil)
		if err != nil {
			return err
		}
		var doc struct {
			Requests []map[string]any `json:"requests"`
		}
		if err = json.Unmarshal(rep.body, &doc); err != nil || len(doc.Requests) == 0 {
			return fmt.Errorf("the journal is empty or unreadable: %v %s", err, truncate(rep.body))
		}
		if _, has := doc.Requests[0][field]; has {
			return fmt.Errorf("the entry has %q: %v", field, doc.Requests[0])
		}
		return nil
	})
	sc.Step(`^I open the unmatched request's near-miss analysis$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		_, err = w.managedAdminCall("GET", "/requests/unmatched/near-misses", "", "", w.setupCred(m.defaultOrg), nil)
		return err
	})
	sc.Step(`^the candidate "([^"]*)" shows source "([^"]*)"$`, func(name, src string) error {
		var doc struct {
			NearMisses []struct {
				StubMapping struct {
					Name string `json:"name"`
				} `json:"stubMapping"`
				Source string `json:"source"`
			} `json:"nearMisses"`
		}
		if err := json.Unmarshal(w.last.body, &doc); err != nil {
			return fmt.Errorf("the near misses are not readable: %w (%s)", err, truncate(w.last.body))
		}
		for _, nm := range doc.NearMisses {
			if nm.StubMapping.Name == name {
				if nm.Source != src {
					return fmt.Errorf("candidate %s shows source %q, want %q", name, nm.Source, src)
				}
				return nil
			}
		}
		return fmt.Errorf("no candidate %q in %s", name, truncate(w.last.body))
	})
	sc.Step(`^a key-based shadow mock "([^"]*)" with its own mapping for "([A-Z]+) ([^"]*)"$`, func(name, method, url string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.record(func() error {
			rep, err := w.managedAdminCall("POST", "/sessions/key-based", "{}", "", w.setupCred(m.defaultOrg), nil)
			if err != nil {
				return err
			}
			if noteErr := w.noteSession(name, rep); noteErr != nil {
				return noteErr
			}
			doc := fmt.Sprintf(`{"request":{"method":%q,"url":%q},"response":{"status":200,"body":"shadow"}}`, method, url)
			rep, err = w.managedAdminCall("POST", "/sessions/"+w.vars[name+".id"]+"/mappings", doc, "", w.setupCred(m.defaultOrg), nil)
			if err != nil {
				return err
			}
			if rep.status != http.StatusCreated {
				return fmt.Errorf("adding the mapping gave %d: %s", rep.status, truncate(rep.body))
			}
			return nil
		})
	})
	sc.Step(`^the SUT sends "([A-Z]+) ([^"]*)" carrying the key of "([^"]*)"$`, func(method, path, name string) error {
		r := newRequest(method, path)
		r.header.Set("X-Shadow-Mock-Key", w.vars[name+".key"])
		_, err := w.send(r)
		return err
	})

	// Isolation.
	sc.Step(`^org "([^"]*)" has a key-based shadow mock "([^"]*)"$`, func(org, name string) error {
		return w.record(func() error {
			rep, err := w.managedAdminCall("POST", "/sessions/key-based", "{}", org, w.setupCred(org), nil)
			if err != nil {
				return err
			}
			return w.noteSession(name, rep)
		})
	})
	sc.Step(`^no session in the response belongs to org "([^"]*)"$`, func(org string) error {
		var doc struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal(w.last.body, &doc); err != nil {
			return err
		}
		for _, s := range doc.Sessions {
			if _, ok := w.app.Service().Sessions.Get(tenant.ID(org), s.ID); ok {
				return fmt.Errorf("session %s belongs to org %s", s.ID, org)
			}
		}
		return nil
	})
	sc.Step(`^a request was sent to "([^"]*)"$`, func(url string) error { _, err := w.send(newRequest("GET", url)); return err })
	sc.Step(`^no journal entry mentions "([^"]*)"$`, func(s string) error {
		if strings.Contains(string(w.last.body), s) {
			return fmt.Errorf("the journal mentions %q: %s", s, truncate(w.last.body))
		}
		return nil
	})
	sc.Step(`^orgs "([^"]*)" and "([^"]*)" each have a global mapping with id "([^"]*)" for "([^"]*)" that returns (\d+) on call (\d+)$`, func(a, b, id, url string, status, call int) error {
		doc := fmt.Sprintf(`{"id":%q,"request":{"url":%q},"response":{"status":200},"callResponses":[{"calls":"%d","response":{"status":%d}}]}`, id, url, call, status)
		for _, org := range []string{a, b} {
			if err := w.record(func() error { return w.putGlobalMappingJSON(org, []byte(doc)) }); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^org "([^"]*)" has called "([^"]*)" (\d+) times$`, func(org, path string, n int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for range n {
			if _, err = w.send(newRequest("GET", "/"+m.orgs[org].handle+path)); err != nil {
				return err
			}
		}
		return nil
	})
}

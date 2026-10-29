package conformance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// adminStep is one admin call sent to both servers. The replies are compared on their status and
// on the values at keys, a dotted path where # is the length of a list. Anything else in a reply
// (ports, times, headers, wording) is allowed to differ.
type adminStep struct {
	name   string
	method string
	path   string
	body   string
	keys   []string
}

// adminKeys picks one value out of a decoded reply: "meta.total", "requests.0.request.url",
// "requests.#".
func adminKeys(v any, path string) any {
	for _, part := range strings.Split(path, ".") {
		switch node := v.(type) {
		case map[string]any:
			v = node[part]
		case []any:
			if part == "#" {
				return float64(len(node))
			}
			i, err := strconv.Atoi(part)
			if err != nil || i >= len(node) {
				return nil
			}
			v = node[i]
		default:
			return nil
		}
	}
	return v
}

// TestAdminAPIMatchesWireMock plays the same admin calls against Shadow Mock and a real WireMock
// and compares what they answer on the points FR-10.7 to FR-10.9 are about. It needs a WireMock
// (see differential_test.go) and leaves it as it found it.
func TestAdminAPIMatchesWireMock(t *testing.T) {
	base := os.Getenv(wiremockURLVar)
	if base == "" {
		t.Skipf("set %s to a WireMock to compare against it", wiremockURLVar)
	}
	ours := start(t, "wiremock")
	theirs := &env{t: t, base: strings.TrimRight(base, "/"), client: ours.client}
	o, w := ours.with(t), theirs.with(t)
	t.Cleanup(func() {
		// The reference server is shared with the corpus comparison: put its stubs, journal and
		// settings back.
		for _, c := range []call{
			{method: "POST", target: "/__admin/mappings/reset"},
			{method: "DELETE", target: "/__admin/requests"},
			{method: "PUT", target: "/__admin/settings", body: `{"proxyPassThrough":true}`},
		} {
			req, err := http.NewRequestWithContext(context.Background(), c.method, theirs.base+c.target, strings.NewReader(c.body))
			if err != nil {
				t.Error(err)
				continue
			}
			resp, err := theirs.client.Do(req)
			if err != nil {
				t.Error(err)
				continue
			}
			resp.Body.Close()
		}
	})

	run := func(t *testing.T, s adminStep) {
		t.Helper()
		t.Run(s.name, func(t *testing.T) {
			c := call{method: s.method, target: "/__admin" + s.path, body: s.body}
			if s.body != "" {
				c.header = hdr("Content-Type", "application/json")
			}
			a, b := o.mustSend(c), w.mustSend(c)
			if a.status != b.status {
				t.Errorf("status: Shadow Mock %d, WireMock %d (%s | %s)", a.status, b.status, abbreviate(string(a.body), 160), abbreviate(string(b.body), 160))
				return
			}
			var x, y any
			_ = json.Unmarshal(a.body, &x)
			_ = json.Unmarshal(b.body, &y)
			for _, k := range s.keys {
				if got, want := adminKeys(x, k), adminKeys(y, k); !reflect.DeepEqual(got, want) {
					t.Errorf("%s: Shadow Mock %v, WireMock %v", k, got, want)
				}
			}
		})
	}
	both := func(c call) {
		o.mustSend(c)
		w.mustSend(c)
	}
	const (
		idA = "aaaaaaaa-0000-4000-8000-000000000001"
		idB = "bbbbbbbb-0000-4000-8000-000000000002"
		idC = "cccccccc-0000-4000-8000-000000000003"
	)
	asJSON := hdr("Content-Type", "application/json")
	fresh := func() {
		both(call{method: "DELETE", target: "/__admin/mappings"})
		both(call{method: "DELETE", target: "/__admin/requests"})
		both(call{method: "POST", target: "/__admin/mappings", header: asJSON, body: `{"id":"` + idA + `","request":{"urlPath":"/adm4/a"},"response":{"status":200,"body":"A"},"metadata":{"team":"claims"}}`})
		both(call{method: "POST", target: "/__admin/mappings", header: asJSON, body: `{"id":"` + idB + `","request":{"method":"GET","urlPath":"/adm4/b"},"response":{"status":201}}`})
		for _, p := range []string{"/adm4/a", "/adm4/b", "/adm4/nomatch1", "/adm4/a", "/adm4/nomatch2"} {
			both(get(p))
		}
	}

	t.Run("journal queries", func(t *testing.T) {
		fresh()
		counts := []string{"requests.#", "meta.total", "requestJournalDisabled"}
		for _, q := range []string{
			"", "?limit=0", "?limit=2", "?limit=50", "?unmatched=true", "?unmatched=false", "?unmatched=maybe",
			"?matchingStub=" + idA, "?matchingStub=" + idB, "?matchingStub=" + idC, "?matchingStub=" + idA + "&unmatched=true",
			"?unmatched=true&limit=1", "?since=2999-01-01T00:00:00Z", "?matchingStub=nope", "?since=yesterday",
		} {
			run(t, adminStep{name: "GET /requests" + q, method: "GET", path: "/requests" + q, keys: counts})
		}
		run(t, adminStep{name: "newest first", method: "GET", path: "/requests?limit=2", keys: []string{"requests.0.request.url", "requests.1.request.url", "requests.0.wasMatched"}})
	})

	t.Run("journal answers", func(t *testing.T) {
		fresh()
		for _, p := range []string{"/requests/find", "/requests/count"} {
			run(t, adminStep{name: "POST " + p, method: "POST", path: p, body: `{"urlPath":"/adm4/a"}`, keys: []string{"requestJournalDisabled", "count", "requests.#"}})
		}
		run(t, adminStep{name: "GET /requests/unmatched", method: "GET", path: "/requests/unmatched", keys: []string{"requestJournalDisabled", "requests.#", "requests.0.url"}})
		run(t, adminStep{name: "sub events of an unmatched request", method: "GET", path: "/requests?unmatched=true&limit=1", keys: []string{"requests.0.subEvents.#", "requests.0.subEvents.0.type", "requests.0.subEvents.0.data.status"}})
		run(t, adminStep{name: "sub events of a matched request", method: "GET", path: "/requests?matchingStub=" + idB, keys: []string{"requests.0.subEvents.#"}})
		run(t, adminStep{name: "delete an unknown id", method: "DELETE", path: "/requests/dddddddd-0000-4000-8000-000000000004", keys: []string{"#"}})
		run(t, adminStep{name: "delete an id that is no UUID", method: "DELETE", path: "/requests/not-a-uuid"})
		run(t, adminStep{name: "get an unknown id", method: "GET", path: "/requests/dddddddd-0000-4000-8000-000000000004"})
		run(t, adminStep{name: "remove by pattern", method: "POST", path: "/requests/remove", body: `{"urlPath":"/adm4/a"}`, keys: []string{"serveEvents.#", "serveEvents.0.wasMatched", "serveEvents.0.request.url"}})
		run(t, adminStep{name: "what is left", method: "GET", path: "/requests", keys: []string{"requests.#", "meta.total"}})
		fresh()
		run(t, adminStep{name: "remove by metadata", method: "POST", path: "/requests/remove-by-metadata", body: `{"matchesJsonPath":"$.team"}`, keys: []string{"serveEvents.#", "serveEvents.0.request.url"}})
		run(t, adminStep{name: "remove by metadata that nothing has", method: "POST", path: "/requests/remove-by-metadata", body: `{"equalTo":"nothing"}`, keys: []string{"serveEvents.#"}})
		run(t, adminStep{name: "what is left by metadata", method: "GET", path: "/requests", keys: []string{"requests.#"}})
	})

	t.Run("mappings", func(t *testing.T) {
		fresh()
		run(t, adminStep{name: "the stored method", method: "GET", path: "/mappings/" + idA, keys: []string{"request.method", "id", "uuid"}})
		run(t, adminStep{name: "create without a method", method: "POST", path: "/mappings", body: `{"request":{"url":"/adm4/c"},"response":{"status":200}}`, keys: []string{"request.method"}})
		// WireMock keeps the mappings it loaded from files when all are deleted, and cannot delete
		// them one by one, so the counts below only agree when it holds none.
		if _, v := w.admin("GET", "/mappings", ""); adminKeys(v, "meta.total") != float64(3) {
			t.Log("the reference WireMock holds mappings loaded from files, so the mapping counts are not compared")
			return
		}
		run(t, adminStep{name: "the alias of the mappings list", method: "GET", path: "/", keys: []string{"mappings.#", "meta.total"}})
		run(t, adminStep{name: "unmatched mappings", method: "GET", path: "/mappings/unmatched", keys: []string{"mappings.#", "meta.total"}})
		fresh()
		run(t, adminStep{name: "delete unmatched mappings", method: "DELETE", path: "/mappings/unmatched"})
		run(t, adminStep{name: "what is left of the mappings", method: "GET", path: "/mappings", keys: []string{"meta.total", "mappings.0.id"}})
	})

	t.Run("settings", func(t *testing.T) {
		shown := []string{"settings.proxyPassThrough", "settings.extended.team", "settings.fixedDelay"}
		run(t, adminStep{name: "put", method: "PUT", path: "/settings", body: `{"proxyPassThrough":false,"extended":{"team":"claims"}}`})
		run(t, adminStep{name: "read the put", method: "GET", path: "/settings", keys: shown})
		run(t, adminStep{name: "post without proxyPassThrough", method: "POST", path: "/settings", body: `{"fixedDelay":5}`})
		run(t, adminStep{name: "read the post", method: "GET", path: "/settings", keys: shown})
		run(t, adminStep{name: "put it back", method: "PUT", path: "/settings", body: `{"proxyPassThrough":true}`})
		run(t, adminStep{name: "read the default", method: "GET", path: "/settings", keys: shown})
	})

	t.Run("documents", func(t *testing.T) {
		for _, p := range []string{"/docs", "/docs/swagger", "/swagger-ui", "/swagger-ui/"} {
			run(t, adminStep{name: "GET " + p, method: "GET", path: p})
		}
	})

	t.Run("strict mappings", func(t *testing.T) {
		for name, m := range map[string]string{
			"request field":          `{"request":{"urlPaht":"/a"},"response":{"status":200}}`,
			"response field":         `{"request":{"url":"/a"},"response":{"statu":200}}`,
			"top-level field":        `{"reqest":{"url":"/a"},"response":{"status":200}}`,
			"priority":               `{"request":{"url":"/a"},"response":{"status":200},"prioriti":1}`,
			"body file":              `{"request":{"url":"/a"},"response":{"bodyFileNam":"x"}}`,
			"delay distribution":     `{"request":{"url":"/a"},"response":{"delayDistribution":{"type":"lognormal","median":1,"sigma":0.1,"medin":1}}}`,
			"another distribution":   `{"request":{"url":"/a"},"response":{"delayDistribution":{"type":"lognormal","median":1,"sigma":0.1,"lower":1}}}`,
			"header matcher":         `{"request":{"url":"/a","headers":{"X":{"equalz":"1"}}},"response":{}}`,
			"body matcher":           `{"request":{"url":"/a","bodyPatterns":[{"equalToJsn":"1"}]},"response":{}}`,
			"a matcher option alone": `{"request":{"url":"/a","headers":{"X":{"caseInsensitive":true}}},"response":{}}`,
			"multipart pattern":      `{"request":{"url":"/a","multipartPatterns":[{"matchingType":"ANY","bodyPatternz":[]}]},"response":{}}`,
			"basic auth":             `{"request":{"url":"/a","basicAuthCredentials":{"username":"a","pasword":"b"}},"response":{}}`,
			"custom matcher":         `{"request":{"url":"/a","customMatcher":{"name":"x","extra":1}},"response":{}}`,
			"metadata is open":       `{"request":{"url":"/a"},"response":{},"metadata":{"anything":{"goes":1}}}`,
			"transformer parameters": `{"request":{"url":"/a"},"response":{"transformerParameters":{"anything":1}},"priority":3}`,
			"webhook parameters":     `{"request":{"url":"/a"},"response":{},"postServeActions":[{"name":"webhook","parameters":{"method":"GET","url":"http://localhost:1/x","extraField":1}}]}`,
			"the usual fields":       `{"name":"n","priority":2,"persistent":false,"request":{"method":"GET","urlPath":"/a","headers":{"X":{"equalTo":"1","caseInsensitive":true}}},"response":{"status":200,"headers":{"A":"b"},"body":"x"}}`,
		} {
			run(t, adminStep{name: name, method: "POST", path: "/mappings", body: m, keys: []string{"errors.0.source.pointer"}})
		}
		run(t, adminStep{name: "an import", method: "POST", path: "/mappings/import", body: `{"mappings":[{"request":{"url":"/ok"},"response":{}},{"request":{"urlPaht":"/a"},"response":{}}]}`, keys: []string{"errors.0.source.pointer"}})
	})
}

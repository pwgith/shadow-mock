package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// recordingServer is the upstream the recorded traffic goes to.
func recordingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasPrefix(r.URL.Path, "/rec-json"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
		case strings.HasPrefix(r.URL.Path, "/rec-bin"):
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{0, 1, 2, 255})
		default:
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("X-Thing", "t")
			fmt.Fprintf(w, "%s %s %s", r.Method, r.URL.RequestURI(), body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// recordedShape removes what is allowed to differ between two recordings of the same traffic: the
// ids, and the Date header.
func recordedShape(m map[string]any) map[string]any {
	delete(m, "id")
	delete(m, "uuid")
	if resp, ok := m["response"].(map[string]any); ok {
		if h, ok := resp["headers"].(map[string]any); ok {
			delete(h, "Date")
		}
	}
	return m
}

// TestRecordingMatchesWireMock proxies the same requests through Shadow Mock and a real WireMock,
// snapshots both and compares the mappings they build: their request patterns, names, bodies and
// order. It needs a WireMock (see differential_test.go) and leaves it as it found it.
func TestRecordingMatchesWireMock(t *testing.T) {
	base := os.Getenv(wiremockURLVar)
	if base == "" {
		t.Skipf("set %s to a WireMock to compare against it", wiremockURLVar)
	}
	ours := start(t, "wiremock")
	theirs := &env{t: t, base: strings.TrimRight(base, "/"), client: ours.client}
	o, w := ours.with(t), theirs.with(t)
	up := recordingServer(t)
	t.Cleanup(func() {
		for _, c := range []call{
			{method: "POST", target: "/__admin/mappings/reset"},
			{method: "DELETE", target: "/__admin/requests"},
		} {
			req, err := http.NewRequestWithContext(context.Background(), c.method, theirs.base+c.target, http.NoBody)
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

	proxyAll := `{"priority":1,"request":{"urlPattern":"/rec-.*"},"response":{"proxyBaseUrl":"` + up.URL + `"}}`
	for _, e := range []*env{o, w} {
		if r, _ := e.admin("POST", "/mappings", proxyAll); r.status != http.StatusCreated {
			t.Fatalf("proxy mapping: %d %s", r.status, r.body)
		}
	}
	traffic := []call{
		{method: "GET", target: "/rec-plain?x=1&y=2"},
		{method: "GET", target: "/rec-plain?x=1&y=2"},
		{method: "GET", target: "/rec-json"},
		{method: "GET", target: "/rec-bin"},
		{method: "GET", target: "/rec-multi?a=1&a=2&b=&c=%20x"},
		{method: "GET", target: "/rec-a/b/c.json?z=1"},
		{method: "GET", target: "/rec-a%20b/c"},
		{method: "POST", target: "/rec-post", body: `{"k":1}`, header: map[string][]string{"Content-Type": {"application/json"}, "X-Key": {"k1"}}},
		{method: "POST", target: "/rec-post", body: `k=2`, header: map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}}},
	}
	for _, c := range traffic {
		o.mustSend(c)
		w.mustSend(c)
	}

	snapshot := func(name, body string) {
		t.Run(name, func(t *testing.T) {
			a, av := ours.with(t).admin("POST", "/recordings/snapshot", body)
			b, bv := theirs.with(t).admin("POST", "/recordings/snapshot", body)
			if a.status != b.status {
				t.Fatalf("status: Shadow Mock %d, WireMock %d (%s | %s)", a.status, b.status, a.body, b.body)
			}
			if _, ids := bv["ids"]; ids {
				if x, y := len(av["ids"].([]any)), len(bv["ids"].([]any)); x != y {
					t.Errorf("ids: Shadow Mock %d, WireMock %d", x, y)
				}
				return
			}
			am, _ := av["mappings"].([]any)
			bm, _ := bv["mappings"].([]any)
			if len(am) != len(bm) {
				t.Fatalf("mappings: Shadow Mock %d, WireMock %d", len(am), len(bm))
			}
			for i := range am {
				x, y := recordedShape(am[i].(map[string]any)), recordedShape(bm[i].(map[string]any))
				// WireMock records scenarios for repeats; Shadow Mock has none.
				for _, k := range []string{"scenarioName", "requiredScenarioState", "newScenarioState"} {
					delete(y, k)
				}
				if !reflect.DeepEqual(x, y) {
					xj, _ := json.Marshal(x)
					yj, _ := json.Marshal(y)
					t.Errorf("mapping %d:\n  Shadow Mock %s\n  WireMock    %s", i, abbreviate(string(xj), 600), abbreviate(string(yj), 600))
				}
			}
		})
	}
	// Repeats are not scenarios here, so WireMock is asked for one mapping per distinct request.
	snapshot("default", `{"persist":false,"repeatsAsScenarios":false,"filters":{"urlPathPattern":"/rec-.*"}}`)
	snapshot("ids", `{"persist":false,"outputFormat":"IDS","repeatsAsScenarios":false,"filters":{"urlPathPattern":"/rec-.*"}}`)
	snapshot("capture headers", `{"persist":false,"repeatsAsScenarios":false,"filters":{"urlPath":"/rec-post"},"captureHeaders":{"X-Key":{"caseInsensitive":false}}}`)
	snapshot("body matching", `{"persist":false,"repeatsAsScenarios":false,"filters":{"urlPath":"/rec-post"},"requestBodyPattern":{"matcher":"equalTo","caseInsensitive":true}}`)
	snapshot("sizes in units", `{"persist":false,"repeatsAsScenarios":false,"filters":{"urlPath":"/rec-json"},"extractBodyCriteria":{"textSizeThreshold":"100 b"}}`)
}

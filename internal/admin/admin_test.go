package admin_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/admin"
	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// api is an engine under test, reached over HTTP.
type api struct {
	t       *testing.T
	base    string
	app     *app.App
	stopped chan struct{}
}

func newAPI(t *testing.T, edit ...func(*config.Config)) *api {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Storage.Global.Driver = "memory"
	for _, e := range edit {
		e(&cfg)
	}
	a := &api{t: t, stopped: make(chan struct{}, 1)}
	var err error
	a.app, err = app.New(cfg, app.Options{
		Logger: slog.New(slog.DiscardHandler), Clock: clock.NewFake(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)),
		Version: "test", Shutdown: func() { a.stopped <- struct{}{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.app.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := a.app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.app.Handler())
	t.Cleanup(srv.Close)
	a.base = srv.URL
	return a
}

// do sends a request. path is relative to /__admin, unless it starts with "@", which sends the
// rest as mock traffic.
func (a *api) do(method, path, body string, hdr ...string) (int, map[string]any, string) {
	a.t.Helper()
	if rest, mock := strings.CutPrefix(path, "@"); mock {
		path = rest
	} else {
		path = "/__admin" + path
	}
	req, err := http.NewRequestWithContext(a.t.Context(), method, a.base+path, strings.NewReader(body))
	if err != nil {
		a.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatal(err)
	}
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	return resp.StatusCode, v, string(b)
}

// want sends a request and fails the test unless the status matches. It returns the JSON body.
func (a *api) want(status int, method, path, body string, hdr ...string) map[string]any {
	a.t.Helper()
	got, v, raw := a.do(method, path, body, hdr...)
	if got != status {
		a.t.Fatalf("%s %s = %d, want %d; body: %s", method, path, got, status, raw)
	}
	return v
}

// wantError sends a request and checks the status and the error code. A "validation_failed" may
// also arrive in WireMock's form, a non-empty "errors" list, which the contract allows.
func (a *api) wantError(status int, code, method, path, body string, hdr ...string) {
	a.t.Helper()
	got, v, raw := a.do(method, path, body, hdr...)
	errs, _ := v["errors"].([]any)
	if code == "validation_failed" && len(errs) > 0 {
		v["error"] = code
	}
	if got != status || v["error"] != code {
		a.t.Errorf("%s %s = %d %v, want %d %s; body: %s", method, path, got, v["error"], status, code, raw)
	}
}

// mock sends mock traffic and returns the status.
func (a *api) mock(method, path string) int {
	a.t.Helper()
	got, _, _ := a.do(method, "@"+path, "")
	return got
}

func mapping(id, path, body string) string {
	return fmt.Sprintf(`{"id":%q,"request":{"urlPath":%q},"response":{"body":%q}}`, id, path, body)
}

const (
	idA = "aaaaaaaa-0000-4000-8000-000000000001"
	idB = "bbbbbbbb-0000-4000-8000-000000000002"
)

func TestSettings(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	if s, _ := a.want(200, "GET", "/settings", "")["settings"].(map[string]any); len(s) != 0 {
		t.Errorf("default settings = %v, want none", s)
	}
	a.want(200, "POST", "/settings", `{"fixedDelay":100,"extended":{"team":"claims"}}`)
	s, _ := a.want(200, "GET", "/settings", "")["settings"].(map[string]any)
	ext, _ := s["extended"].(map[string]any)
	if s["fixedDelay"] != float64(100) || ext["team"] != "claims" {
		t.Errorf("settings = %v", s)
	}
	a.wantError(422, "validation_failed", "POST", "/settings", `{"fixedDelay":-1}`)
	a.wantError(422, "validation_failed", "POST", "/settings", `{"fixedDelay":"soon"}`)
	a.wantError(422, "validation_failed", "POST", "/settings", `not json`)
}

func TestUsageAndVersion(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.wantError(404, "not_supported", "GET", "/usage", "")
	if v := a.want(200, "GET", "/version", ""); v["version"] != "test" {
		t.Errorf("version = %v", v)
	}
	a.wantError(404, "not_found", "GET", "/no/such/endpoint", "")
	a.wantError(404, "not_found", "GET", "/mappings/validate", "") // wrong method for a known path
}

func TestLiveStats(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.want(201, "POST", "/mappings", mapping(idA, "/ok", "x"))
	a.mock("GET", "/ok")
	a.mock("GET", "/ok")
	a.mock("GET", "/missing")
	v := a.want(200, "GET", "/stats/live?window=5m", "")
	if v["window"] != "5m" || v["unmatched"] != float64(1) || v["matchRate"] == float64(0) {
		t.Errorf("stats = %v", v)
	}
	if per, _ := v["requestsPerMinute"].([]any); len(per) == 0 {
		t.Errorf("requestsPerMinute is empty: %v", v)
	}
	for _, window := range []string{"", "15m", "1h"} {
		a.want(200, "GET", "/stats/live?window="+window, "")
	}
	a.wantError(422, "validation_failed", "GET", "/stats/live?window=2h", "")
	a.wantError(422, "validation_failed", "GET", "/stats/live?window=forever", "")
}

func TestRecordingStateMachine(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	status := func(path string) any { return a.want(200, "GET", path, "")["status"] }
	for _, base := range []string{"/recordings", "/sessions/" + a.want(201, "POST", "/sessions/key-based", `{}`)["id"].(string) + "/recordings"} {
		if got := status(base + "/status"); got != "NeverStarted" {
			t.Errorf("%s: initial status = %v", base, got)
		}
		a.want(200, "POST", base+"/start", `{"targetBaseUrl":"http://localhost:8089"}`)
		if got := status(base + "/status"); got != "Recording" {
			t.Errorf("%s: after start = %v", base, got)
		}
		a.wantError(409, "recording_in_progress", "POST", base+"/start", `{"targetBaseUrl":"http://localhost:8089"}`)
		if m, _ := a.want(200, "POST", base+"/stop", "")["mappings"].([]any); m == nil || len(m) != 0 {
			t.Errorf("%s: stop returns %v, want an empty list", base, m)
		}
		if got := status(base + "/status"); got != "Stopped" {
			t.Errorf("%s: after stop = %v", base, got)
		}
		a.want(200, "POST", base+"/start", `{"targetBaseUrl":"http://localhost:8089"}`) // can start again once stopped
	}
	a.want(200, "POST", "/recordings/snapshot", `{}`)
	a.wantError(422, "validation_failed", "POST", "/recordings/start", `not json`)
	a.wantError(404, "not_found", "GET", "/sessions/ses_00000000000000000000000000/recordings/status", "")
}

func TestJournalEndpoints(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.want(201, "POST", "/mappings", mapping(idA, "/one", "1"))
	a.mock("GET", "/one")
	a.mock("POST", "/two")
	a.mock("GET", "/one")

	list := a.want(200, "GET", "/requests", "")
	reqs, _ := list["requests"].([]any)
	if len(reqs) != 3 || list["requestJournalDisabled"] != false {
		t.Fatalf("journal = %v", list)
	}
	if limited := a.want(200, "GET", "/requests?limit=2", ""); len(limited["requests"].([]any)) != 2 {
		t.Errorf("limit=2 returned %v", limited["requests"])
	}
	if future := a.want(200, "GET", "/requests?since=2099-01-01T00:00:00Z", ""); len(future["requests"].([]any)) != 0 {
		t.Errorf("since in the future returned %v", future["requests"])
	}
	a.wantError(422, "validation_failed", "GET", "/requests?since=yesterday", "")

	first, _ := reqs[0].(map[string]any)
	id, _ := first["id"].(string)
	if got := a.want(200, "GET", "/requests/"+id, ""); got["id"] != id {
		t.Errorf("GET one = %v", got)
	}
	a.want(200, "DELETE", "/requests/"+id, "")
	a.wantError(404, "not_found", "GET", "/requests/"+id, "")
	a.wantError(404, "not_found", "DELETE", "/requests/"+id, "")

	if got := a.want(200, "POST", "/requests/count", `{"method":"GET","urlPath":"/one"}`); got["count"] != float64(1) {
		t.Errorf("count = %v", got)
	}
	a.wantError(422, "validation_failed", "POST", "/requests/count", `{"url":"/a","urlPath":"/a"}`)
	a.wantError(422, "validation_failed", "POST", "/requests/find", `{"urlPattern":"("}`)
	removed := a.want(200, "POST", "/requests/remove", `{"method":"POST"}`)
	if r, _ := removed["requests"].([]any); len(r) != 1 {
		t.Errorf("remove returned %v", removed)
	}
	if got := a.want(200, "GET", "/requests/unmatched", ""); len(got["requests"].([]any)) != 0 {
		t.Errorf("unmatched = %v", got)
	}
	a.want(200, "DELETE", "/requests", "")
	if got := a.want(200, "GET", "/requests", ""); len(got["requests"].([]any)) != 0 {
		t.Errorf("after clearing: %v", got)
	}
}

func TestNearMissEndpoints(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.want(201, "POST", "/mappings", `{"id":"`+idA+`","name":"get claim","request":{"method":"GET","urlPath":"/claims/1","headers":{"Accept":{"equalTo":"application/json"}}},"response":{}}`)
	a.mock("GET", "/claims/1") // no Accept header: a near miss
	a.mock("GET", "/claims/1")

	nm := a.want(200, "POST", "/near-misses/request", `{"method":"GET","url":"/claims/1"}`)
	list, _ := nm["nearMisses"].([]any)
	first, _ := list[0].(map[string]any)
	stub, _ := first["stubMapping"].(map[string]any)
	if len(list) != 1 || stub["id"] != idA || first["layer"] != "global" {
		t.Errorf("near misses for a request = %v", nm)
	}
	a.wantError(422, "validation_failed", "POST", "/near-misses/request", `{"method":"GET"}`)
	a.wantError(422, "validation_failed", "POST", "/near-misses/request", `nope`)

	un := a.want(200, "GET", "/requests/unmatched/near-misses", "")
	if l, _ := un["nearMisses"].([]any); len(l) != 2 {
		t.Errorf("near misses for the two unmatched requests = %v", un)
	}

	pat := a.want(200, "POST", "/near-misses/request-pattern", `{"method":"GET","urlPath":"/claims/2"}`)
	pl, _ := pat["nearMisses"].([]any)
	if len(pl) == 0 {
		t.Fatalf("near misses for a pattern = %v", pat)
	}
	if e, _ := pl[0].(map[string]any); e["request"] == nil || e["requestPattern"] == nil {
		t.Errorf("a pattern near miss names the request and the pattern: %v", e)
	}
	a.wantError(422, "validation_failed", "POST", "/near-misses/request-pattern", `{"urlPattern":"("}`)
}

func TestCounterEndpoints(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.want(201, "POST", "/mappings", `{"id":"`+idA+`","request":{"urlPath":"/c"},"response":{"status":200},"callResponses":[{"calls":"3","response":{"status":500}}]}`)
	a.mock("GET", "/c")
	a.mock("GET", "/c")

	cs, _ := a.want(200, "GET", "/counters", "")["counters"].([]any)
	first, _ := cs[0].(map[string]any)
	if len(cs) != 1 || first["stubMappingId"] != idA || first["count"] != float64(2) || first["stubLayer"] != "global" || first["lastCalledAt"] == nil {
		t.Errorf("counters = %v", cs)
	}
	set := a.want(200, "PUT", "/counters/"+idA, `{"count":7,"stubLayer":"global"}`)
	if set["count"] != float64(7) {
		t.Errorf("set counter = %v", set)
	}
	a.wantError(422, "validation_failed", "PUT", "/counters/"+idA, `{}`)
	a.wantError(422, "validation_failed", "PUT", "/counters/"+idA, `{"count":-1}`)
	a.wantError(422, "validation_failed", "PUT", "/counters/"+idA, `nope`)
	a.wantError(404, "not_found", "PUT", "/counters/"+idB, `{"count":1}`)
	a.wantError(404, "not_found", "PUT", "/counters/"+idA, `{"count":1,"stubLayer":"key-based"}`)

	a.want(200, "DELETE", "/counters/"+idA, "")
	a.wantError(404, "not_found", "DELETE", "/counters/"+idB, "")
	if cs, _ := a.want(200, "GET", "/counters", "")["counters"].([]any); len(cs) != 0 {
		t.Errorf("after reset: %v", cs)
	}
	a.mock("GET", "/c")
	a.want(200, "POST", "/counters/reset", "")
	if cs, _ := a.want(200, "GET", "/counters", "")["counters"].([]any); len(cs) != 0 {
		t.Errorf("after reset all: %v", cs)
	}
}

func TestMappingEndpoints(t *testing.T) {
	// The subtests share one engine and its journal, so they run in order.
	a := newAPI(t)
	a.want(201, "POST", "/mappings", `{"id":"`+idA+`","request":{"urlPath":"/a"},"response":{"body":"v1"},"metadata":{"team":"claims"}}`)
	a.want(201, "POST", "/mappings", `{"id":"`+idB+`","request":{"urlPath":"/b"},"response":{"body":"b"},"metadata":{"team":"billing"}}`)
	a.mock("GET", "/a")

	t.Run("unmatched mappings are those no request has hit", func(t *testing.T) {
		ms, _ := a.want(200, "GET", "/mappings/unmatched", "")["mappings"].([]any)
		if len(ms) != 1 || ms[0].(map[string]any)["id"] != idB {
			t.Errorf("unmatched mappings = %v", ms)
		}
	})
	t.Run("find and remove by metadata", func(t *testing.T) {
		found, _ := a.want(200, "POST", "/mappings/find-by-metadata", `{"matchesJsonPath":{"expression":"$.team","equalTo":"billing"}}`)["mappings"].([]any)
		if len(found) != 1 {
			t.Errorf("found %v", found)
		}
		a.wantError(422, "validation_failed", "POST", "/mappings/find-by-metadata", `{"bogus":1}`)
		a.wantError(422, "validation_failed", "POST", "/mappings/remove-by-metadata", `{"bogus":1}`)
		a.wantError(422, "validation_failed", "POST", "/mappings/find-by-metadata", `nope`)
		a.want(200, "POST", "/mappings/remove-by-metadata", `{"matchesJsonPath":{"expression":"$.team","equalTo":"billing"}}`)
		a.wantError(404, "not_found", "GET", "/mappings/"+idB, "")
		a.want(201, "POST", "/mappings", `{"id":"`+idB+`","request":{"urlPath":"/b"},"response":{"body":"b"}}`)
	})
	t.Run("validate always answers 200 and explains what is wrong", func(t *testing.T) {
		if v := a.want(200, "POST", "/mappings/validate", mapping(idA, "/ok", "x")); v["valid"] != true {
			t.Errorf("valid mapping: %v", v)
		}
		for _, bad := range []string{`[1]`, `{"request":{"url":"/a","urlPath":"/a"},"response":{}}`, `{"scenarioName":"s","request":{},"response":{}}`, `not json`} {
			v := a.want(200, "POST", "/mappings/validate", bad)
			if errs, _ := v["errors"].([]any); v["valid"] != false || len(errs) == 0 {
				t.Errorf("validate(%s) = %v, want valid:false with errors", bad, v)
			}
		}
	})
	t.Run("replace and delete need an existing mapping", func(t *testing.T) {
		a.wantError(404, "not_found", "PUT", "/mappings/"+"cccccccc-0000-4000-8000-000000000003", mapping("x", "/x", "x"))
		a.wantError(404, "not_found", "DELETE", "/mappings/"+"cccccccc-0000-4000-8000-000000000003", "")
		a.wantError(422, "validation_failed", "PUT", "/mappings/"+idA, `{"request":{"urlPattern":"("},"response":{}}`)
		if got := a.want(200, "PUT", "/mappings/"+idA, mapping("ignored", "/a", "v2")); got["id"] != idA {
			t.Errorf("replace keeps the url's id: %v", got)
		}
		if a.mock("GET", "/a") != 200 {
			t.Error("the replaced mapping no longer serves")
		}
	})
	t.Run("list pages", func(t *testing.T) {
		v := a.want(200, "GET", "/mappings?limit=1&offset=1", "")
		if ms, _ := v["mappings"].([]any); len(ms) != 1 || v["meta"].(map[string]any)["total"] != float64(2) {
			t.Errorf("paged list = %v", v)
		}
		if ms, _ := a.want(200, "GET", "/mappings?offset=5", "")["mappings"].([]any); len(ms) != 0 {
			t.Errorf("offset beyond the end = %v", ms)
		}
	})
	t.Run("import honours the duplicate policy and reports the bad mapping", func(t *testing.T) {
		a.want(200, "POST", "/mappings/import", `{"mappings":[`+mapping(idA, "/a", "kept")+`],"importOptions":{"duplicatePolicy":"IGNORE"}}`)
		if got := a.want(200, "GET", "/mappings/"+idA, ""); got["response"].(map[string]any)["body"] != "v2" {
			t.Errorf("IGNORE replaced the existing mapping: %v", got)
		}
		a.want(200, "POST", "/mappings/import", `{"mappings":[`+mapping(idA, "/a", "overwritten")+`]}`)
		if got := a.want(200, "GET", "/mappings/"+idA, ""); got["response"].(map[string]any)["body"] != "overwritten" {
			t.Errorf("OVERWRITE kept the old mapping: %v", got)
		}
		a.want(200, "POST", "/mappings/import", `{"mappings":[`+mapping(idA, "/a", "only")+`],"importOptions":{"deleteAllNotInImport":true}}`)
		if ms, _ := a.want(200, "GET", "/mappings", "")["mappings"].([]any); len(ms) != 1 {
			t.Errorf("deleteAllNotInImport left %v", ms)
		}
		got, v, _ := a.do("POST", "/mappings/import", `{"mappings":[`+mapping("d", "/d", "d")+`,{"request":{"url":"/a","urlPath":"/a"},"response":{}}]}`)
		errs, _ := v["errors"].([]any)
		src, _ := errs[0].(map[string]any)["source"].(map[string]any)
		if got != 422 || !strings.HasPrefix(fmt.Sprint(src["pointer"]), "/mappings/1") {
			t.Errorf("import with a bad mapping = %d %v, want 422 pointing at /mappings/1", got, v)
		}
		if a.mock("GET", "/d") != 404 {
			t.Error("a failed import must apply nothing")
		}
		a.wantError(422, "validation_failed", "POST", "/mappings/import", `nope`)
	})
	t.Run("reset and save", func(_ *testing.T) {
		a.want(200, "POST", "/mappings/save", "")
		a.want(200, "POST", "/mappings/reset", "")
		a.want(200, "DELETE", "/mappings", "")
		a.want(200, "POST", "/reset", "")
	})
}

func TestFileEndpoints(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	if got, _, _ := a.do("PUT", "/files/sub/greeting.txt", "hello"); got != 200 {
		t.Fatalf("PUT = %d", got)
	}
	if _, _, raw := a.do("GET", "/files/sub/greeting.txt", ""); raw != "hello" {
		t.Errorf("GET = %q", raw)
	}
	if list, _, raw := a.do("GET", "/files", ""); list != 200 || !strings.Contains(raw, "sub/greeting.txt") {
		t.Errorf("list = %d %s", list, raw)
	}
	a.want(200, "DELETE", "/files/sub/greeting.txt", "")
	a.wantError(404, "not_found", "GET", "/files/sub/greeting.txt", "")
	a.wantError(404, "not_found", "DELETE", "/files/sub/greeting.txt", "")
}

func TestSessionEndpoints(t *testing.T) {
	// The subtests share one engine and its session list, so they run in order.
	a := newAPI(t)
	unknown := "ses_00000000000000000000000000"

	t.Run("creation rejects what it cannot use", func(_ *testing.T) {
		a.wantError(422, "validation_failed", "POST", "/sessions/key-based", `nope`)
		a.wantError(422, "validation_failed", "POST", "/sessions/key-based", `{"ttlSeconds":"soon"}`)
		a.wantError(422, "validation_failed", "POST", "/sessions/url-based", `{"tokens":["TOKEN-123"]}`)
		a.wantError(422, "validation_failed", "POST", "/sessions/url-based", `{"parentId":"abc"}`)
		a.wantError(422, "validation_failed", "POST", "/sessions/key-based", `{"masks":[{}]}`)
		a.wantError(422, "validation_failed", "POST", "/sessions/key-based", `{"masks":[{"request":{"urlPattern":"("}}]}`)
		a.wantError(422, "validation_failed", "POST", "/sessions/key-based", `{"seedFrom":{"mappingSet":"nope"}}`)
		a.wantError(422, "parent_not_found", "POST", "/sessions/key-based", `{"parentId":"aaaaaaaaaaaaaaaa"}`)
	})
	t.Run("listing filters and pages", func(t *testing.T) {
		for range 3 {
			a.want(201, "POST", "/sessions/key-based", `{}`)
		}
		a.want(201, "POST", "/sessions/url-based", `{}`)
		all := a.want(200, "GET", "/sessions", "")
		if all["meta"].(map[string]any)["total"] != float64(4) {
			t.Errorf("total = %v", all["meta"])
		}
		if s, _ := a.want(200, "GET", "/sessions?kind=key-based&limit=2", "")["sessions"].([]any); len(s) != 2 {
			t.Errorf("limit 2 returned %d", len(s))
		}
		if s, _ := a.want(200, "GET", "/sessions?kind=url-based", "")["sessions"].([]any); len(s) != 1 {
			t.Errorf("url-based returned %d", len(s))
		}
		a.wantError(422, "validation_failed", "GET", "/sessions?kind=other", "")
	})
	t.Run("an unknown session is a 404 on every session endpoint", func(_ *testing.T) {
		for _, c := range [][3]string{
			{"GET", "/sessions/" + unknown, ""},
			{"PATCH", "/sessions/" + unknown, `{}`},
			{"DELETE", "/sessions/" + unknown, ""},
			{"POST", "/sessions/" + unknown + "/keepalive", ""},
			{"GET", "/sessions/" + unknown + "/mappings", ""},
			{"GET", "/sessions/" + unknown + "/counters", ""},
		} {
			a.wantError(404, "not_found", c[0], c[1], c[2])
		}
	})
	t.Run("a patch must be valid", func(_ *testing.T) {
		id := a.want(201, "POST", "/sessions/key-based", `{}`)["id"].(string)
		a.wantError(422, "validation_failed", "PATCH", "/sessions/"+id, `nope`)
		a.wantError(422, "validation_failed", "PATCH", "/sessions/"+id, `{"addMasks":[{}]}`)
		a.wantError(422, "ttl_exceeds_maximum", "PATCH", "/sessions/"+id, `{"ttlSeconds":999999}`)
		a.wantError(422, "token_too_short", "PATCH", "/sessions/"+id, `{"addTokens":["ab"]}`)
	})
}

func TestDryRun(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.want(201, "POST", "/mappings", mapping(idA, "/pay", "global"))
	s := a.want(201, "POST", "/sessions/key-based", `{"mappings":[`+mapping(idB, "/pay", "session")+`]}`)
	u := a.want(201, "POST", "/sessions/url-based", `{}`)

	global := a.want(200, "POST", "/mappings/dry-run", `{"request":{"method":"GET","url":"/pay"}}`)
	if global["matched"] != true || global["layer"] != "global" || global["sessionId"] != nil || global["response"].(map[string]any)["body"] != "global" {
		t.Errorf("global dry run = %v", global)
	}
	viaSession := a.want(200, "POST", "/mappings/dry-run", `{"sessionId":"`+s["id"].(string)+`","request":{"method":"GET","url":"/pay"}}`)
	if viaSession["layer"] != "key-based" || viaSession["sessionId"] != s["id"] || viaSession["response"].(map[string]any)["body"] != "session" {
		t.Errorf("session dry run = %v", viaSession)
	}
	if v := a.want(200, "POST", "/mappings/dry-run", `{"urlMockId":"`+u["id"].(string)+`","request":{"method":"GET","url":"/pay"}}`); v["layer"] != "global" || v["sessionId"] != nil {
		t.Errorf("URL-based dry run falling through = %v", v)
	}
	miss := a.want(200, "POST", "/mappings/dry-run", `{"request":{"method":"GET","url":"/nothing"}}`)
	if nm, _ := miss["nearMisses"].([]any); miss["matched"] != false || len(nm) == 0 {
		t.Errorf("unmatched dry run = %v", miss)
	}
	a.wantError(404, "not_found", "POST", "/mappings/dry-run", `{"sessionId":"ses_00000000000000000000000000","request":{"method":"GET","url":"/pay"}}`)
	a.wantError(422, "validation_failed", "POST", "/mappings/dry-run", `{"request":{"method":"GET"}}`)
	a.wantError(422, "validation_failed", "POST", "/mappings/dry-run", `nope`)
	// A dry run leaves no trace.
	if reqs, _ := a.want(200, "GET", "/requests", "")["requests"].([]any); len(reqs) != 0 {
		t.Errorf("dry runs wrote %d journal entries", len(reqs))
	}
}

func TestScopeHeader(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	s := a.want(201, "POST", "/sessions/key-based", `{}`)
	key, id := s["key"].(string), s["id"].(string)
	for _, ref := range []string{key, id} { // the header takes the key or the id
		a.want(201, "POST", "/mappings", mapping("", "/scoped", "x"), "X-Shadow-Mock-Key", ref)
	}
	if ms, _ := a.want(200, "GET", "/sessions/"+id+"/mappings", "")["mappings"].([]any); len(ms) != 2 {
		t.Errorf("session mappings = %d, want 2", len(ms))
	}
	if ms, _ := a.want(200, "GET", "/mappings", "")["mappings"].([]any); len(ms) != 0 {
		t.Errorf("the global session got %d mappings", len(ms))
	}
	a.wantError(404, "not_found", "GET", "/mappings", "", "X-Shadow-Mock-Key", "smk_00000000000000000000000000")
}

func TestBodyLimit(t *testing.T) {
	t.Parallel()
	a := newAPI(t, func(c *config.Config) { c.Limits.MaxBodyBytes = 120 })
	a.want(201, "POST", "/mappings", mapping(idA, "/a", "small"))
	a.wantError(413, "body_too_large", "POST", "/mappings", mapping(idB, "/b", strings.Repeat("x", 300)))
	a.wantError(413, "body_too_large", "PUT", "/files/big.bin", strings.Repeat("x", 300))
}

func TestGlobalWritesReservedForTheManagementApp(t *testing.T) {
	t.Parallel()
	a := newAPI(t, func(c *config.Config) { c.Admin.GlobalWrites = "deny" })
	for _, w := range [][3]string{
		{"POST", "/mappings", mapping(idA, "/a", "x")},
		{"DELETE", "/mappings", ""},
		{"POST", "/mappings/import", `{"mappings":[]}`},
		{"PUT", "/files/x.txt", "x"},
		{"POST", "/settings", `{}`},
		{"POST", "/reset", ""},
		{"POST", "/recordings/start", `{}`},
		{"POST", "/shutdown", ""},
	} {
		a.wantError(403, "global_writes_via_management_app", w[0], w[1], w[2])
	}
	// Reads, sessions and everything scoped to a session stay open.
	a.want(200, "GET", "/mappings", "")
	id := a.want(201, "POST", "/sessions/key-based", `{}`)["id"].(string)
	a.want(201, "POST", "/sessions/"+id+"/mappings", mapping(idA, "/a", "x"))
	a.want(200, "POST", "/sessions/"+id+"/recordings/start", `{"targetBaseUrl":"http://localhost:8089"}`)
	a.want(200, "DELETE", "/requests", "")
}

func TestShutdown(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.want(200, "POST", "/shutdown", "")
	select {
	case <-a.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not stop the engine")
	}
	// Without a shutdown hook (SaaS), the endpoint does not exist.
	noHook, err := app.New(func() config.Config {
		c := config.Default()
		c.Storage.Global.Driver = "memory"
		c.Root = t.TempDir()
		return c
	}(),
		app.Options{Logger: slog.New(slog.DiscardHandler), Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = noHook.Close() })
	if err := noHook.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	noHook.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/__admin/shutdown", http.NoBody))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "not_available_in_saas") {
		t.Errorf("shutdown without a hook = %d %s", rec.Code, rec.Body)
	}
}

// stubAuth authenticates as a fixed principal, or fails.
type stubAuth struct {
	p   auth.Principal
	err error
}

func (s stubAuth) Authenticate(*http.Request) (auth.Principal, error) { return s.p, s.err }

func TestAuthenticationAndAuthorisation(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	call := func(authn auth.Authenticator, authz auth.Authorizer, method, path string) (int, string) {
		h := admin.New(a.app.Service(), authn, authz, admin.Config{Version: "test"}, slog.New(slog.DiscardHandler))
		req := httptest.NewRequestWithContext(tenant.With(t.Context(), tenant.Local), method, "/__admin"+path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	person := func(org tenant.ID, role auth.Role) auth.Principal {
		return auth.Principal{UserID: "u", OrgID: org, Roles: []auth.Role{role}}
	}
	authz := auth.RoleAuthorizer{}

	t.Run("an authentication failure is passed through", func(t *testing.T) {
		t.Parallel()
		code, body := call(stubAuth{err: apierr.New(401, apierr.InvalidToken, "bad token")}, authz, "GET", "/mappings")
		if code != 401 || !strings.Contains(body, "invalid_token") {
			t.Errorf("%d %s", code, body)
		}
	})
	t.Run("a token for another org is refused before anything runs", func(t *testing.T) {
		t.Parallel()
		code, body := call(stubAuth{p: person("globex", auth.Admin)}, authz, "GET", "/mappings")
		if code != 403 || !strings.Contains(body, "org_mismatch") {
			t.Errorf("%d %s", code, body)
		}
	})
	t.Run("a viewer may read but not write", func(t *testing.T) {
		t.Parallel()
		viewer := stubAuth{p: person(tenant.Local, auth.Viewer)}
		if code, body := call(viewer, authz, "GET", "/mappings"); code != 200 {
			t.Errorf("read: %d %s", code, body)
		}
		for _, w := range [][2]string{{"POST", "/sessions/key-based"}, {"POST", "/mappings"}, {"DELETE", "/requests"}} {
			if code, body := call(viewer, authz, w[0], w[1]); code != 403 || !strings.Contains(body, `"forbidden"`) {
				t.Errorf("%s %s: %d %s, want 403 forbidden", w[0], w[1], code, body)
			}
		}
	})
	t.Run("an editor manages sessions but not the global session", func(t *testing.T) {
		t.Parallel()
		editor := stubAuth{p: person(tenant.Local, auth.Editor)}
		if code, body := call(editor, authz, "POST", "/sessions/key-based"); code != 201 {
			t.Errorf("create session: %d %s", code, body)
		}
		if code, _ := call(editor, authz, "POST", "/mappings"); code != 403 {
			t.Errorf("global write: %d, want 403", code)
		}
	})
}

// A journal entry carries the fields WireMock's does, which clients read: the body as text and as
// base64 (always), the protocol, scheme, host and port, and the parsed form fields.
func TestJournalEntriesCarryWireMocksFields(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.want(201, "POST", "/mappings", `{"request":{"method":"POST","urlPath":"/f"},"response":{"status":201,"body":"done"}}`)
	a.do("POST", "@/f", "k=v&k=w", "Content-Type", "application/x-www-form-urlencoded")
	entry := a.want(200, "GET", "/requests", "")["requests"].([]any)[0].(map[string]any)
	req := entry["request"].(map[string]any)

	if req["body"] != "k=v&k=w" || req["bodyAsBase64"] != "az12Jms9dw==" {
		t.Errorf("request body %q and base64 %q", req["body"], req["bodyAsBase64"])
	}
	if req["protocol"] != "HTTP/1.1" || req["scheme"] != "http" || req["host"] == "" || req["port"] == nil || req["browserProxyRequest"] != false {
		t.Errorf("request address fields: %v", req)
	}
	form, _ := req["formParams"].(map[string]any)
	if k, _ := form["k"].(map[string]any); k["key"] != "k" || len(k["values"].([]any)) != 2 {
		t.Errorf("formParams = %v", form)
	}
	resp := entry["response"].(map[string]any)
	if resp["body"] != "done" || resp["bodyAsBase64"] != "ZG9uZQ==" {
		t.Errorf("response body %q and base64 %q", resp["body"], resp["bodyAsBase64"])
	}
}

func TestJournalEntriesForEmptyBodiesStillHaveBothBodyFields(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	a.want(201, "POST", "/mappings", `{"request":{"urlPath":"/e"},"response":{"status":204}}`)
	a.mock("GET", "/e")
	entry := a.want(200, "GET", "/requests", "")["requests"].([]any)[0].(map[string]any)
	for name, part := range map[string]any{"request": entry["request"], "response": entry["response"]} {
		m := part.(map[string]any)
		if body, ok := m["body"]; !ok || body != "" {
			t.Errorf("%s body = %v (present %v), want an empty string", name, body, ok)
		}
		if b64, ok := m["bodyAsBase64"]; !ok || b64 != "" {
			t.Errorf("%s bodyAsBase64 = %v (present %v), want an empty string", name, b64, ok)
		}
	}
}

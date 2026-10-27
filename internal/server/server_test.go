package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/server"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

func request(t *testing.T, remote string, hdr ...string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = remote
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Add(hdr[i], hdr[i+1])
	}
	return r
}

func TestClientIP(t *testing.T) {
	t.Parallel()
	prefixes := func(s ...string) []netip.Prefix {
		p, err := server.ParsePrefixes(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name string
		cfg  server.ClientIP
		req  *http.Request
		want string // "" means an invalid address
	}{
		{"the TCP peer when no header is configured", server.ClientIP{}, request(t, "203.0.113.7:5555", "X-Forwarded-For", "1.2.3.4"), "203.0.113.7"},
		{"an IPv6 peer", server.ClientIP{}, request(t, "[2001:db8::1]:5555"), "2001:db8::1"},
		{"an IPv4-mapped peer is unmapped", server.ClientIP{}, request(t, "[::ffff:10.0.0.9]:1"), "10.0.0.9"},
		{"a peer without a port", server.ClientIP{}, request(t, "10.1.1.1"), "10.1.1.1"},
		{"an unparseable peer", server.ClientIP{}, request(t, "not-an-address"), ""},
		{"the header is absent", server.ClientIP{Header: "X-Forwarded-For"}, request(t, "10.0.0.1:1"), "10.0.0.1"},
		{
			"one trusted hop takes the rightmost entry",
			server.ClientIP{Header: "X-Forwarded-For", TrustedHops: 1},
			request(t, "10.0.0.1:1", "X-Forwarded-For", "9.9.9.9, 8.8.8.8"), "8.8.8.8",
		},
		{
			"zero hops is treated as one",
			server.ClientIP{Header: "X-Forwarded-For"},
			request(t, "10.0.0.1:1", "X-Forwarded-For", "9.9.9.9, 8.8.8.8"), "8.8.8.8",
		},
		{
			"two trusted hops",
			server.ClientIP{Header: "X-Forwarded-For", TrustedHops: 2},
			request(t, "10.0.0.1:1", "X-Forwarded-For", "9.9.9.9, 8.8.8.8, 7.7.7.7"), "8.8.8.8",
		},
		{
			"more hops than entries falls back to the peer",
			server.ClientIP{Header: "X-Forwarded-For", TrustedHops: 5},
			request(t, "10.0.0.1:1", "X-Forwarded-For", "9.9.9.9"), "10.0.0.1",
		},
		{
			"several header lines are read as one list",
			server.ClientIP{Header: "X-Forwarded-For", TrustedHops: 1},
			request(t, "10.0.0.1:1", "X-Forwarded-For", "9.9.9.9", "X-Forwarded-For", "8.8.8.8"), "8.8.8.8",
		},
		{
			"an unparseable entry falls back to the peer",
			server.ClientIP{Header: "X-Forwarded-For", TrustedHops: 1},
			request(t, "10.0.0.1:1", "X-Forwarded-For", "9.9.9.9, garbage"), "10.0.0.1",
		},
		{
			"an IPv4-mapped entry is unmapped",
			server.ClientIP{Header: "X-Forwarded-For", TrustedHops: 1},
			request(t, "10.0.0.1:1", "X-Forwarded-For", "::ffff:8.8.8.8"), "8.8.8.8",
		},
		{
			"trusted proxies are skipped from the right",
			server.ClientIP{Header: "X-Forwarded-For", TrustedProxies: prefixes("10.0.0.0/8")},
			request(t, "10.0.0.9:1", "X-Forwarded-For", "9.9.9.9, 10.0.0.1, 10.0.0.2"), "9.9.9.9",
		},
		{
			"a spoofed leftmost entry is ignored",
			server.ClientIP{Header: "X-Forwarded-For", TrustedProxies: prefixes("10.0.0.0/8")},
			request(t, "10.0.0.9:1", "X-Forwarded-For", "1.1.1.1, 9.9.9.9, 10.0.0.1"), "9.9.9.9",
		},
		{
			"every hop trusted falls back to the peer",
			server.ClientIP{Header: "X-Forwarded-For", TrustedProxies: prefixes("10.0.0.0/8")},
			request(t, "10.0.0.9:1", "X-Forwarded-For", "10.0.0.1, 10.0.0.2"), "10.0.0.9",
		},
		{
			"garbage among trusted proxies falls back to the peer",
			server.ClientIP{Header: "X-Forwarded-For", TrustedProxies: prefixes("10.0.0.0/8")},
			request(t, "10.0.0.9:1", "X-Forwarded-For", "garbage, 10.0.0.1"), "10.0.0.9",
		},
		{
			"a single-value platform header",
			server.ClientIP{Header: "Fly-Client-IP", TrustedHops: 1},
			request(t, "10.0.0.9:1", "Fly-Client-IP", "198.51.100.4"), "198.51.100.4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.cfg.From(tt.req)
			if tt.want == "" {
				if got.IsValid() {
					t.Errorf("From = %v, want an invalid address", got)
				}
				return
			}
			if got.String() != tt.want {
				t.Errorf("From = %v, want %s", got, tt.want)
			}
		})
	}
}

func TestParsePrefixes(t *testing.T) {
	t.Parallel()
	got, err := server.ParsePrefixes([]string{" 10.1.2.3/8 ", "203.0.113.7", "2001:db8::/32", "::ffff:192.0.2.1", "2001:db8::5"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "203.0.113.7/32", "2001:db8::/32", "192.0.2.1/32", "2001:db8::5/128"}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("prefix %d = %s, want %s", i, p, want[i])
		}
	}
	for _, bad := range []string{"10.0.0.0/33", "300.1.1.1", "not-an-ip", "10.0.0.0/x"} {
		if _, err := server.ParsePrefixes([]string{bad}); err == nil {
			t.Errorf("ParsePrefixes(%q) succeeded", bad)
		}
	}
	if got, err := server.ParsePrefixes(nil); err != nil || len(got) != 0 {
		t.Errorf("ParsePrefixes(nil) = %v, %v", got, err)
	}
}

// newRouter returns a router built the way cmd/shadowmock builds one, for tests to adjust.
func newRouter(t *testing.T) (*server.Router, *app.App) {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Storage.Global.Driver = "memory"
	a, err := app.New(cfg, app.Options{Logger: slog.New(slog.DiscardHandler), Clock: clock.NewFake(time.Unix(1_800_000_000, 0)), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	rt, ok := a.Handler().(*server.Router)
	if !ok {
		t.Fatalf("the handler is a %T, want *server.Router", a.Handler())
	}
	cp := *rt
	return &cp, a
}

func serve(t *testing.T, h http.Handler, target, remote string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	if remote != "" {
		r.RemoteAddr = remote
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func jsonBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return v
}

func TestHealthAndMetrics(t *testing.T) {
	t.Parallel()
	rt, _ := newRouter(t)
	for _, path := range []string{"/__health", "/__ready", "/__admin/health"} {
		rec := serve(t, rt, path, "")
		if rec.Code != 200 || jsonBody(t, rec)["status"] != "healthy" {
			t.Errorf("GET %s = %d %s", path, rec.Code, rec.Body)
		}
	}
	if checks, _ := jsonBody(t, serve(t, rt, "/__ready", ""))["checks"].(map[string]any); checks["storage"] != "up" {
		t.Errorf("readiness checks = %v", checks)
	}
	rec := serve(t, rt, "/__metrics", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "shadowmock_active_sessions 0") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("metrics = %d %q (%s)", rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
	rt.Metrics = nil
	if rec := serve(t, rt, "/__metrics", ""); rec.Code != 404 {
		t.Errorf("hidden metrics = %d, want 404", rec.Code)
	}
}

func TestOrgSegment(t *testing.T) {
	t.Parallel()
	rt, _ := newRouter(t)
	rt.Directory = tenant.NewStatic(
		&tenant.Org{ID: tenant.Local, Handle: "acme"},
		&tenant.Org{ID: "org_other", Handle: "globex"},
	)
	rt.RequireOrgSegment = true

	t.Run("a known handle routes to its org", func(t *testing.T) {
		t.Parallel()
		if rec := serve(t, rt, "/acme/__admin/version", ""); rec.Code != 200 || jsonBody(t, rec)["version"] != "test" {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
		// With no mapping the org still answers with the unmatched diagnostic, so the org was found.
		if rec := serve(t, rt, "/acme/anything", ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "Request was not matched") {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("an unknown handle is unknown_org", func(t *testing.T) {
		t.Parallel()
		for _, path := range []string{"/nobody/anything", "/", "/anything"} {
			rec := serve(t, rt, path, "")
			if rec.Code != 404 || jsonBody(t, rec)["error"] != "unknown_org" {
				t.Errorf("GET %s = %d %s, want 404 unknown_org", path, rec.Code, rec.Body)
			}
		}
	})
	t.Run("a token for one org cannot act on another", func(t *testing.T) {
		t.Parallel()
		// The standalone authenticator always yields the local org, so globex is a different org.
		rec := serve(t, rt, "/globex/__admin/version", "")
		if rec.Code != 403 || jsonBody(t, rec)["error"] != "org_mismatch" {
			t.Errorf("%d %s, want 403 org_mismatch", rec.Code, rec.Body)
		}
	})
	t.Run("a URL-based shadow mock lives under the org prefix", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/acme/__admin/sessions/url-based", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Host = "mock.example"
		rt.ServeHTTP(rec, req)
		v := jsonBody(t, rec)
		if rec.Code != 201 || v["baseUrl"] != "http://mock.example/acme/d/"+v["id"].(string) {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		through := serve(t, rt, "/acme/d/"+v["id"].(string)+"/x", "")
		if through.Code != 404 || strings.Contains(through.Body.String(), "unknown_url_based_mock") {
			t.Errorf("through the URL insert: %d %s", through.Code, through.Body)
		}
		if rec := serve(t, rt, "/acme/d/aaaaaaaaaaaaaaaa/x", ""); jsonBody(t, rec)["error"] != "unknown_url_based_mock" {
			t.Errorf("unknown id: %d %s", rec.Code, rec.Body)
		}
		// "/d/" with no id is an ordinary path.
		if rec := serve(t, rt, "/acme/d/", ""); strings.Contains(rec.Body.String(), "unknown_url_based_mock") {
			t.Errorf("an empty id was treated as a URL-based mock: %s", rec.Body)
		}
	})
}

func TestIPAllowlist(t *testing.T) {
	t.Parallel()
	allow, err := server.ParsePrefixes([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	build := func(t *testing.T, admin bool) (*server.Router, *app.App) {
		t.Helper()
		rt, a := newRouter(t)
		rt.Directory = tenant.NewStatic(&tenant.Org{ID: tenant.Local, Handle: "local", Allowlist: allow, AllowlistAdmin: admin})
		return rt, a
	}

	t.Run("mock traffic from outside is refused before anything else happens", func(t *testing.T) {
		t.Parallel()
		rt, a := build(t, false)
		rec := serve(t, rt, "/anything", "192.0.2.1:4000")
		v := jsonBody(t, rec)
		details, _ := v["details"].(map[string]any)
		if rec.Code != 403 || v["error"] != "ip_not_allowed" || details["ip"] != "192.0.2.1" {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
		if serve(t, rt, "/anything", "10.1.1.1:4000").Code != 404 { // allowed: reaches the unmatched 404
			t.Error("an allowed address was refused")
		}
		live := a.Service().Stats.Live(tenant.Local, "5m", 5*time.Minute, time.Unix(1_800_000_000, 0), stats.ActiveSessions{})
		if len(live.IPRejections) != 1 || live.IPRejections[0].IP != "192.0.2.1" {
			t.Errorf("rejections = %+v, want the refused address counted", live.IPRejections)
		}
	})
	t.Run("the admin API is exempt unless the org asks for it to be covered", func(t *testing.T) {
		t.Parallel()
		open, _ := build(t, false)
		if rec := serve(t, open, "/__admin/version", "192.0.2.1:4000"); rec.Code != 200 {
			t.Errorf("exempt admin: %d %s", rec.Code, rec.Body)
		}
		covered, _ := build(t, true)
		if rec := serve(t, covered, "/__admin/version", "192.0.2.1:4000"); rec.Code != 403 || jsonBody(t, rec)["error"] != "ip_not_allowed" {
			t.Errorf("covered admin: %d %s", rec.Code, rec.Body)
		}
	})
}

func TestListenerSplit(t *testing.T) {
	t.Parallel()
	mockOnly, _ := newRouter(t)
	mockOnly.ServeAdmin = false
	adminOnly, _ := newRouter(t)
	adminOnly.ServeMock = false

	for _, tt := range []struct {
		name string
		rt   *server.Router
		path string
		want int
	}{
		{"mock listener serves mock traffic", mockOnly, "/anything", 404}, // the unmatched 404 means it was handled
		{"mock listener refuses admin", mockOnly, "/__admin/version", 404},
		{"admin listener serves admin", adminOnly, "/__admin/version", 200},
		{"admin listener refuses mock traffic", adminOnly, "/anything", 404},
		{"health is served by every listener", adminOnly, "/__health", 200},
		{"WireMock's health endpoint is served by every listener", mockOnly, "/__admin/health", 200},
	} {
		rec := serve(t, tt.rt, tt.path, "")
		if rec.Code != tt.want {
			t.Errorf("%s: GET %s = %d, want %d", tt.name, tt.path, rec.Code, tt.want)
		}
	}
	if !strings.Contains(serve(t, mockOnly, "/__admin/version", "").Body.String(), "does not serve") {
		t.Error("the refusal should say that this listener does not serve the path")
	}
	if strings.Contains(serve(t, adminOnly, "/anything", "").Body.String(), "Request was not matched") {
		t.Error("the admin listener must not run mock matching")
	}
}

func TestAdminPrefix(t *testing.T) {
	t.Parallel()
	rt, _ := newRouter(t)
	rt.AdminPrefix = "/__control"
	if rec := serve(t, rt, "/__control/version", ""); rec.Code != 200 {
		t.Errorf("GET /__control/version = %d %s", rec.Code, rec.Body)
	}
	if rec := serve(t, rt, "/__control", ""); rec.Code == 200 && jsonBody(t, rec)["version"] != nil {
		t.Errorf("the bare prefix should not be a version endpoint: %s", rec.Body)
	}
	// The default prefix is no longer the admin API, so it is mock traffic.
	if rec := serve(t, rt, "/__admin/version", ""); !strings.Contains(rec.Body.String(), "Request was not matched") {
		t.Errorf("GET /__admin/version = %d %s, want mock traffic", rec.Code, rec.Body)
	}
	// A path that merely starts with the prefix is not under it.
	if rec := serve(t, rt, "/__controller/version", ""); !strings.Contains(rec.Body.String(), "Request was not matched") {
		t.Errorf("GET /__controller/version = %d %s", rec.Code, rec.Body)
	}
}

func TestNewServerTimeouts(t *testing.T) {
	t.Parallel()
	srv := server.New(":0", http.NotFoundHandler(), 5*time.Minute)
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Errorf("timeouts must be set (§12): %+v", srv)
	}
	if srv.WriteTimeout <= 5*time.Minute {
		t.Errorf("WriteTimeout = %v, want more than the longest mock delay", srv.WriteTimeout)
	}
}

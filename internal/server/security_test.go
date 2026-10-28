package server_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/server"
)

func send(t *testing.T, h http.Handler, method, target string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	r.Host = "mocks.example.com"
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// The admin API has no login in standalone mode, so a web page must not be able to drive it
// through a visitor's browser.
func TestAdminRefusesChangesFromOtherSites(t *testing.T) {
	t.Parallel()
	rt, _ := newRouter(t)
	for _, tt := range []struct {
		name   string
		method string
		hdr    []string
		want   int
	}{
		{"a command-line client sends neither header", http.MethodPost, nil, 200},
		{"a page on the same origin", http.MethodPost, []string{"Origin", "http://mocks.example.com", "Sec-Fetch-Site", "same-origin"}, 200},
		{"the browser address bar", http.MethodPost, []string{"Sec-Fetch-Site", "none"}, 200},
		{"a page on another site", http.MethodPost, []string{"Origin", "http://evil.example", "Sec-Fetch-Site", "cross-site"}, 403},
		{"a sibling site", http.MethodPost, []string{"Sec-Fetch-Site", "same-site"}, 403},
		{"Origin alone is enough", http.MethodPost, []string{"Origin", "http://evil.example"}, 403},
		{"a sandboxed page sends Origin null", http.MethodPost, []string{"Origin", "null"}, 403},
		{"deletes are covered", http.MethodDelete, []string{"Origin", "http://evil.example"}, 403},
		{"puts are covered", http.MethodPut, []string{"Sec-Fetch-Site", "cross-site"}, 403},
		{"reads are left to the browser's own cross-origin rules", http.MethodGet, []string{"Sec-Fetch-Site", "cross-site"}, 200},
	} {
		target := "/__admin/reset"
		if tt.method == http.MethodGet {
			target = "/__admin/version"
		}
		if tt.method == http.MethodDelete || tt.method == http.MethodPut {
			target = "/__admin/mappings/aaaaaaaa-0000-0000-0000-000000000001"
		}
		rec := send(t, rt, tt.method, target, tt.hdr...)
		if tt.want == 403 {
			if rec.Code != 403 || !strings.Contains(rec.Body.String(), "forbidden") {
				t.Errorf("%s: %s %s = %d %s, want 403 forbidden", tt.name, tt.method, target, rec.Code, rec.Body)
			}
			continue
		}
		if rec.Code == 403 {
			t.Errorf("%s: %s %s was refused: %s", tt.name, tt.method, target, rec.Body)
		}
	}
}

// With the admin API on its own listener, nothing on the mock listener may reach it, however the
// path is written.
func TestAdminIsNotReachableOnTheMockListener(t *testing.T) {
	t.Parallel()
	mockOnly, _ := newRouter(t)
	mockOnly.ServeAdmin = false
	for _, target := range []string{
		"/__admin/version", "//__admin/version", "/./__admin/version", "/x/../__admin/version",
		"/%5F%5Fadmin/version", "/__admin%2Fversion", "/__ADMIN/version", "/__admin./version",
		"/__admin/../__admin/version", "/local/__admin/version", "/d/abc/__admin/version",
		"/__admin/version/", "/%2e%2e/__admin/version",
	} {
		rec := send(t, mockOnly, http.MethodGet, target)
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "version") {
			t.Errorf("GET %s reached the admin API on the mock listener", target)
		}
	}
}

const (
	adminToken  = "admin-secret-0123456789"
	editorToken = "editor-secret-0123456789"
	viewerToken = "viewer-secret-0123456789"
)

func tokenRouter(t *testing.T, edit ...func(*config.Config)) *server.Router {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Storage.Global.Driver = "memory"
	cfg.Auth.Driver = "token"
	cfg.Auth.Tokens = []config.AdminToken{
		{Name: "ops", Role: "admin", Token: adminToken},
		{Name: "ci", Role: "editor", Token: editorToken},
		{Name: "dash", Role: "viewer", Token: viewerToken},
	}
	for _, e := range edit {
		e(&cfg)
	}
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
		t.Fatalf("the handler is a %T", a.Handler())
	}
	return rt
}

func bearerOf(token string) []string {
	if token == "" {
		return nil
	}
	return []string{"Authorization", "Bearer " + token}
}

func TestTheAdminAPINeedsATokenAndTheRoleLimitsWhatItMayDo(t *testing.T) {
	t.Parallel()
	rt := tokenRouter(t)
	mapping := `{"request":{"urlPath":"/x"},"response":{"status":200}}`
	for _, tt := range []struct {
		name, method, path, body, token string
		want                            int
	}{
		{"no token", "GET", "/__admin/mappings", "", "", 401},
		{"a wrong token", "GET", "/__admin/mappings", "", "nope-nope-nope-nope", 401},
		{"a viewer reads", "GET", "/__admin/mappings", "", viewerToken, 200},
		{"a viewer cannot create a session", "POST", "/__admin/sessions/key-based", "{}", viewerToken, 403},
		{"an editor creates a session", "POST", "/__admin/sessions/key-based", "{}", editorToken, 201},
		{"an editor cannot change the global mappings", "POST", "/__admin/mappings", mapping, editorToken, 403},
		{"an admin changes the global mappings", "POST", "/__admin/mappings", mapping, adminToken, 201},
		{"an admin reads", "GET", "/__admin/version", "", adminToken, 200},
	} {
		r := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
		for i, h := range bearerOf(tt.token) {
			if i%2 == 0 {
				r.Header.Set(h, bearerOf(tt.token)[i+1])
			}
		}
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, r)
		if rec.Code != tt.want {
			t.Errorf("%s: %s %s = %d %s, want %d", tt.name, tt.method, tt.path, rec.Code, rec.Body, tt.want)
		}
		if tt.want == 401 && !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: a 401 should say how to authenticate, header = %q", tt.name, rec.Header().Get("WWW-Authenticate"))
		}
	}
}

func TestMockTrafficAndHealthNeedNoToken(t *testing.T) {
	t.Parallel()
	rt := tokenRouter(t)
	for _, path := range []string{"/__health", "/__ready", "/__admin/health", "/anything-mock"} {
		if rec := send(t, rt, http.MethodGet, path); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Errorf("GET %s = %d, want it open", path, rec.Code)
		}
	}
}

func TestTheMetricsNeedATokenWhenTheAdminAPIDoes(t *testing.T) {
	t.Parallel()
	rt := tokenRouter(t)
	if rec := send(t, rt, http.MethodGet, "/__metrics"); rec.Code != http.StatusUnauthorized {
		t.Errorf("metrics without a token = %d, want 401", rec.Code)
	}
	if rec := send(t, rt, http.MethodGet, "/__metrics", "Authorization", "Bearer "+viewerToken); rec.Code != http.StatusOK {
		t.Errorf("metrics with a viewer token = %d, want 200", rec.Code)
	}
	open, _ := newRouter(t)
	if rec := send(t, open, http.MethodGet, "/__metrics"); rec.Code != http.StatusOK {
		t.Errorf("metrics with no admin tokens configured = %d, want 200", rec.Code)
	}
}

// A page reaches a private address through DNS rebinding, which changes what a name points to and
// not the name the browser sends. With a list of host names, any other name is refused.
func TestTheAdminAPIAnswersOnlyToListedHostNames(t *testing.T) {
	t.Parallel()
	rt := tokenRouter(t, func(c *config.Config) { c.Admin.AllowedHosts = []string{"mocks.example.com", "localhost"} })
	for _, tt := range []struct {
		host string
		want int
	}{
		{"mocks.example.com", 200},
		{"MOCKS.example.com:9090", 200},
		{"localhost:8080", 200},
		{"evil.example", 403},
		{"mocks.example.com.evil.example", 403},
		{"127.0.0.1:8080", 403},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__admin/version", http.NoBody)
		r.Host = tt.host
		r.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, r)
		if rec.Code != tt.want {
			t.Errorf("Host %q = %d, want %d", tt.host, rec.Code, tt.want)
		}
	}
	m := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__metrics", http.NoBody)
	m.Host = "evil.example"
	m.Header.Set("Authorization", "Bearer "+viewerToken)
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, m)
	if rec.Code != http.StatusForbidden {
		t.Errorf("metrics for a host that is not listed = %d, want 403", rec.Code)
	}
}

func TestAListedOriginMayMakeChangesFromABrowser(t *testing.T) {
	t.Parallel()
	rt := tokenRouter(t, func(c *config.Config) { c.Admin.AllowedOrigins = []string{"https://dashboard.example.com/"} })
	for _, tt := range []struct {
		name string
		hdr  []string
		want int
	}{
		{"the listed origin", []string{"Origin", "https://dashboard.example.com", "Sec-Fetch-Site", "cross-site"}, 200},
		{"the listed origin in another case", []string{"Origin", "HTTPS://Dashboard.Example.com"}, 200},
		{"another origin", []string{"Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"}, 403},
		{"a look-alike origin", []string{"Origin", "https://dashboard.example.com.evil.example"}, 403},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/__admin/reset", http.NoBody)
		r.Header.Set("Authorization", "Bearer "+adminToken)
		for i := 0; i+1 < len(tt.hdr); i += 2 {
			r.Header.Set(tt.hdr[i], tt.hdr[i+1])
		}
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, r)
		if rec.Code != tt.want {
			t.Errorf("%s: %d %s, want %d", tt.name, rec.Code, rec.Body, tt.want)
		}
	}
}

func TestTheAllowlistCanCoverTheAdminAPI(t *testing.T) {
	t.Parallel()
	rt := tokenRouter(t, func(c *config.Config) {
		c.Tenancy.Local.IPAllowlist = []string{"10.0.0.0/8"}
		c.Tenancy.Local.AllowlistAdmin = true
	})
	for _, tt := range []struct {
		ip   string
		want int
	}{{"10.1.2.3:5000", 200}, {"192.168.1.9:5000", 403}} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__admin/version", http.NoBody)
		r.RemoteAddr = tt.ip
		r.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, r)
		if rec.Code != tt.want {
			t.Errorf("admin call from %s = %d, want %d", tt.ip, rec.Code, tt.want)
		}
	}
}

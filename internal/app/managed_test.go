package app_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/auth/authtest"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

var epoch = time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

// managed is a managed-mode engine, with an issuer it trusts.
type managed struct {
	t      *testing.T
	app    *app.App
	srv    *httptest.Server
	issuer *authtest.Issuer
	clk    *clock.Fake
}

func newManaged(t *testing.T, change func(*config.Config)) *managed {
	t.Helper()
	clk := clock.NewFake(epoch)
	return newManagedWith(t, clk, authtest.NewIssuer("management-app", "https://shadowmock.example", "mocks-test", clk), change)
}

// newManagedWith starts an engine that trusts the issuer, so that two engines can trust the same one.
func newManagedWith(t *testing.T, clk clock.Clock, issuer *authtest.Issuer, change func(*config.Config)) *managed {
	t.Helper()
	cfg := config.Default()
	cfg.Mode, cfg.Root = "managed", t.TempDir()
	cfg.Storage.Global.Watch = false
	cfg.Managed.SingleInstance = true
	cfg.Admin.GlobalWrites, cfg.Mappings.UnknownFields, cfg.Egress.BlockPrivate = "deny", "keep", true
	cfg.Auth.Issuers = []config.IssuerConfig{{Name: "management-app", Issuer: issuer.URL, Audience: issuer.Audience, PublicKey: issuer.PublicPEM()}}
	if change != nil {
		change(&cfg)
	}
	a, err := app.New(cfg, app.Options{Logger: slog.New(slog.DiscardHandler), Clock: clk, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err = a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	fake, _ := clk.(*clock.Fake)
	// fake is nil for a real clock, which tests do not advance.
	return &managed{t: t, app: a, srv: srv, issuer: issuer, clk: fake}
}

func (m *managed) org(handle string) {
	m.t.Helper()
	if err := m.app.Directory().Put(tenant.Org{ID: tenant.ID(handle), Handle: handle}); err != nil {
		m.t.Fatal(err)
	}
}

func (m *managed) do(method, path, credential, body string) (int, string, http.Header) {
	m.t.Helper()
	req, err := http.NewRequestWithContext(m.t.Context(), method, m.srv.URL+path, strings.NewReader(body))
	if err != nil {
		m.t.Fatal(err)
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func errorOf(body string) string {
	var v struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &v)
	return v.Error
}

func TestTokensAreCheckedInOrder(t *testing.T) {
	t.Parallel()
	m := newManaged(t, nil)
	m.org("acme")
	m.org("globex")
	other := authtest.NewIssuer("management-app", m.issuer.URL, m.issuer.Audience, m.clk)
	tests := []struct {
		name   string
		cred   string
		status int
		code   string
	}{
		{"a valid token", m.issuer.TokenFor("alice", "acme", "member", nil), 200, ""},
		{"a signature from another key", other.TokenFor("alice", "acme", "member", nil), 401, "invalid_token"},
		{"alg none", authtest.Unsigned(authtest.Claims{"iss": m.issuer.URL, "aud": m.issuer.Audience, "org_id": "acme", "app_role": "admin"}), 401, "invalid_token"},
		{"another issuer", m.issuer.TokenFor("alice", "acme", "member", authtest.Claims{"iss": "https://evil.example"}), 401, "untrusted_issuer"},
		{"another audience", m.issuer.TokenFor("alice", "acme", "member", authtest.Claims{"aud": "staging"}), 401, "invalid_audience"},
		{"expired", m.issuer.TokenFor("alice", "acme", "member", authtest.Claims{"exp": epoch.Add(-time.Minute).Unix(), "iat": epoch.Add(-2 * time.Minute).Unix()}), 401, "token_expired"},
		{"not yet valid", m.issuer.TokenFor("alice", "acme", "member", authtest.Claims{"nbf": epoch.Add(time.Hour).Unix()}), 401, "token_not_active"},
		{"too long", m.issuer.TokenFor("alice", "acme", "member", authtest.Claims{"exp": epoch.Add(time.Hour).Unix()}), 401, "token_too_long"},
		{"no org", m.issuer.TokenFor("alice", "", "member", authtest.Claims{"org_id": nil}), 403, "no_active_org"},
		{"another org", m.issuer.TokenFor("alice", "globex", "member", nil), 403, "org_mismatch"},
		{"no credential", "", 401, "invalid_token"},
		{"not a token", "nonsense", 401, "invalid_token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, body, _ := m.do("GET", "/acme/__admin/sessions", tt.cred, "")
			if code != tt.status || errorOf(body) != tt.code {
				t.Errorf("got %d %q (%s), want %d %q", code, errorOf(body), body, tt.status, tt.code)
			}
		})
	}
}

func TestAReplayedTokenIsRefusedWhenAskedFor(t *testing.T) {
	t.Parallel()
	m := newManaged(t, func(c *config.Config) { c.Auth.Issuers[0].ReplayCheck = true })
	m.org("acme")
	tok := m.issuer.TokenFor("alice", "acme", "member", nil)
	if code, _, _ := m.do("GET", "/acme/__admin/sessions", tok, ""); code != 200 {
		t.Fatalf("first use: %d", code)
	}
	if code, body, _ := m.do("GET", "/acme/__admin/sessions", tok, ""); code != 401 || errorOf(body) != "token_replayed" {
		t.Errorf("second use: %d %s", code, body)
	}
}

func TestToolAppKeys(t *testing.T) {
	t.Parallel()
	m := newManaged(t, nil)
	m.org("acme")
	m.org("globex")
	keys := m.app.Keys()
	rw := authtest.NewKey(keys, "acme", "ci-tests", "sessions", "read")
	val := authtest.NewKey(keys, "acme", "validator", "validate")
	ro := authtest.NewKey(keys, "acme", "reader", "read")
	valid := `{"request":{"url":"/a"},"response":{"status":200}}`
	tests := []struct {
		name         string
		key          string
		method, path string
		body         string
		status       int
		code         string
	}{
		{"a sessions key lists", rw, "GET", "/acme/__admin/sessions", "", 200, ""},
		{"a sessions key creates", rw, "POST", "/acme/__admin/sessions/key-based", "{}", 201, ""},
		{"a read key lists", ro, "GET", "/acme/__admin/sessions", "", 200, ""},
		{"a read key cannot create", ro, "POST", "/acme/__admin/sessions/key-based", "{}", 403, "forbidden"},
		{"a validate key validates", val, "POST", "/acme/__admin/mappings/validate", valid, 200, ""},
		{"a validate key cannot create", val, "POST", "/acme/__admin/sessions/key-based", "{}", 403, "forbidden"},
		{"a sessions key cannot validate", rw, "POST", "/acme/__admin/mappings/validate", valid, 403, "forbidden"},
		{"no key writes the global layer", rw, "POST", "/acme/__admin/mappings", valid, 403, "global_writes_via_management_app"},
		{"no key writes settings", rw, "POST", "/acme/__admin/settings", "{}", 403, "global_writes_via_management_app"},
		{"no key clears the global journal", rw, "DELETE", "/acme/__admin/requests", "", 403, "forbidden"},
		{"another org", rw, "GET", "/globex/__admin/sessions", "", 403, "org_mismatch"},
		{"a made-up key", authtest.RandomKey(), "GET", "/acme/__admin/sessions", "", 401, "invalid_token"},
		{"a mock set key", "smk_ABCDEFGHIJKLMNOP_" + strings.Repeat("A", 52), "GET", "/acme/__admin/sessions", "", 401, "invalid_token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, body, _ := m.do(tt.method, tt.path, tt.key, tt.body)
			if code != tt.status || errorOf(body) != tt.code {
				t.Errorf("got %d %q (%s), want %d %q", code, errorOf(body), body, tt.status, tt.code)
			}
		})
	}
	t.Run("the label is the creator", func(t *testing.T) {
		_, body, _ := m.do("POST", "/acme/__admin/sessions/key-based", rw, "{}")
		var s struct {
			CreatedBy    string `json:"createdBy"`
			CreatedByKey string `json:"createdByKey"`
		}
		if err := json.Unmarshal([]byte(body), &s); err != nil || s.CreatedBy != "ci-tests" || s.CreatedByKey != "ci-tests" {
			t.Errorf("creator = %+v (%v)", s, err)
		}
	})
	t.Run("a revoked key stops within a minute", func(t *testing.T) {
		key, hash := authtest.NewKeyWith(keys, "acme", "temp", time.Time{}, "read")
		if code, _, _ := m.do("GET", "/acme/__admin/sessions", key, ""); code != 200 {
			t.Fatalf("before: %d", code)
		}
		keys.Revoke(hash)
		if code, _, _ := m.do("GET", "/acme/__admin/sessions", key, ""); code != 200 {
			t.Errorf("a cached key should still work for up to a minute, got %d", code)
		}
		m.clk.Advance(61 * time.Second)
		if code, _, _ := m.do("GET", "/acme/__admin/sessions", key, ""); code != 401 {
			t.Errorf("after a minute: %d", code)
		}
	})
	t.Run("an expired key", func(t *testing.T) {
		key, _ := authtest.NewKeyWith(keys, "acme", "old", m.clk.Now().Add(-time.Hour), "read")
		if code, _, _ := m.do("GET", "/acme/__admin/sessions", key, ""); code != 401 {
			t.Errorf("got %d", code)
		}
	})
	t.Run("use is recorded", func(t *testing.T) {
		key, hash := authtest.NewKeyWith(keys, "acme", "used", time.Time{}, "read")
		m.do("GET", "/acme/__admin/sessions", key, "")
		if use, ok := keys.LastUse(hash); !ok || use.At.IsZero() {
			t.Errorf("last use = %+v %v", use, ok)
		}
	})
}

func TestTheMetadataScopeShowsNoContent(t *testing.T) {
	t.Parallel()
	m := newManaged(t, nil)
	m.org("acme")
	admin := m.issuer.TokenFor("dana", "acme", "member", nil)
	_, body, _ := m.do("POST", "/acme/__admin/sessions/key-based", admin, `{"name":"S1","tokens":["secret-token-1"],"mappings":[{"request":{"url":"/x"},"response":{"status":200,"body":"top secret"}}]}`)
	var s struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &s)
	support := m.issuer.TokenFor("sam", "acme", "viewer", authtest.Claims{"scope": "metadata"})

	for _, p := range []string{"/acme/__admin/sessions", "/acme/__admin/sessions/" + s.ID, "/acme/__admin/requests", "/acme/__admin/usage", "/acme/__admin/version"} {
		code, out, _ := m.do("GET", p, support, "")
		if code != 200 {
			t.Errorf("GET %s under the scope: %d %s", p, code, out)
		}
		for _, secret := range []string{"top secret", "secret-token-1"} {
			if strings.Contains(out, secret) {
				t.Errorf("GET %s leaked %q: %s", p, secret, out)
			}
		}
	}
	type call struct{ method, path string }
	refused := []call{
		{"GET", "/acme/__admin/mappings"},
		{"GET", "/acme/__admin/sessions/" + s.ID + "/mappings"},
		{"POST", "/acme/__admin/requests/find"},
		{"POST", "/acme/__admin/mappings/dry-run"},
		{"POST", "/acme/__admin/sessions/key-based"},
		{"DELETE", "/acme/__admin/sessions/" + s.ID},
		{"POST", "/acme/__admin/settings"},
		{"GET", "/acme/__admin/near-misses"},
		{"POST", "/acme/__admin/org/purge"},
	}
	for _, tt := range refused {
		code, out, _ := m.do(tt.method, tt.path, support, "{}")
		if code != 403 || errorOf(out) != "scope_metadata" {
			t.Errorf("%s %s under the scope: %d %s", tt.method, tt.path, code, out)
		}
	}
}

func TestSuspendedAndDeletedOrgs(t *testing.T) {
	t.Parallel()
	m := newManaged(t, nil)
	m.org("acme")
	m.org("globex")
	dir := m.app.Directory()
	tok := m.issuer.TokenFor("alice", "acme", "member", nil)
	key := authtest.NewKey(m.app.Keys(), "acme", "ci", "sessions", "read")

	if code, _, _ := m.do("GET", "/acme/hello", "", ""); code != 404 {
		t.Errorf("an unknown path of an active org: %d", code)
	}
	_ = dir.SetStatus("acme", tenant.StatusSuspended)
	code, body, hdr := m.do("GET", "/acme/hello", "", "")
	if code != 403 || errorOf(body) != "org_suspended" || hdr.Get("X-Shadow-Mock-Reason") != "org_suspended" {
		t.Errorf("suspended traffic: %d %s %v", code, body, hdr)
	}
	for _, cred := range []string{tok, key} {
		if code, _, _ = m.do("GET", "/acme/__admin/sessions", cred, ""); code != 403 {
			t.Errorf("suspended admin: %d", code)
		}
	}
	if code, _, _ = m.do("GET", "/globex/hello", "", ""); code != 404 {
		t.Errorf("another org must not be affected: %d", code)
	}
	_ = dir.SetStatus("acme", tenant.StatusActive)
	if code, _, _ = m.do("GET", "/acme/hello", "", ""); code != 404 {
		t.Errorf("serving resumes: %d", code)
	}
	_ = dir.MarkDeleted("acme")
	for _, c := range []struct{ path, cred string }{{"/acme/hello", ""}, {"/acme/__admin/sessions", tok}, {"/acme/__admin/sessions", key}} {
		code, body, _ = m.do("GET", c.path, c.cred, "")
		if code != 410 || errorOf(body) != "org_deleted" {
			t.Errorf("deleted %s: %d %s", c.path, code, body)
		}
	}
	if err := dir.SetStatus("acme", tenant.StatusActive); err == nil {
		t.Error("a deleted org must not come back")
	}
}

func TestPerOrgRateLimitAndMonthlyCap(t *testing.T) {
	t.Parallel()
	m := newManaged(t, nil)
	m.org("acme")
	m.org("globex")
	m.org("initech")
	_ = m.app.Directory().Update("acme", func(o *tenant.Org) { o.Limits = tenant.Limits{RequestsPerSecond: 10, Burst: 20} })

	limited := 0
	for range 100 {
		code, body, hdr := m.do("GET", "/acme/hello", "", "")
		if code == 429 {
			limited++
			if errorOf(body) != "rate_limited" || hdr.Get("Retry-After") == "" || hdr.Get("X-Shadow-Mock-Limit") != "requests-per-second" {
				t.Fatalf("a 429 must say why: %s %v", body, hdr)
			}
		}
	}
	if limited < 70 {
		t.Errorf("want most of 100 requests limited at 10/s burst 20, got %d", limited)
	}
	if code, _, _ := m.do("GET", "/globex/hello", "", ""); code != 404 {
		t.Errorf("another org must not be limited: %d", code)
	}
	tok := m.issuer.TokenFor("alice", "acme", "member", nil)
	if code, _, _ := m.do("GET", "/acme/__admin/sessions", tok, ""); code != 200 {
		t.Errorf("admin has a bucket of its own: %d", code)
	}
	m.clk.Advance(5 * time.Second)

	_, body, _ := m.do("GET", "/acme/__admin/usage", tok, "")
	var u struct {
		Buckets []struct {
			Requests    int64 `json:"requests"`
			RateLimited int64 `json:"rateLimited"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal([]byte(body), &u); err != nil || len(u.Buckets) != 1 {
		t.Fatalf("usage = %s (%v)", body, err)
	}
	if u.Buckets[0].RateLimited != int64(limited) || u.Buckets[0].Requests != int64(100-limited) {
		t.Errorf("usage = %+v, want %d limited and %d served", u.Buckets[0], limited, 100-limited)
	}

	// The monthly cap, with a grace.
	_ = m.app.Directory().Update("initech", func(o *tenant.Org) { o.Limits = tenant.Limits{MonthlyRequestCap: 3, Grace: 2} })
	codes := make([]int, 0, 7)
	for range 7 {
		c, b, _ := m.do("GET", "/initech/hello", "", "")
		codes = append(codes, c)
		if c == 429 && errorOf(b) != "monthly_request_cap" {
			t.Errorf("cap error = %s", b)
		}
	}
	want := []int{404, 404, 404, 404, 404, 429, 429}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v, want %v", codes, want)
		}
	}
}

func TestPurgeAnonymiseAndAudit(t *testing.T) {
	t.Parallel()
	m := newManaged(t, nil)
	m.org("acme")
	dana := m.issuer.TokenFor("dana", "acme", "member", nil)
	system := m.issuer.TokenFor("management-app", "acme", "admin", authtest.Claims{"scope": "system"})
	for range 2 {
		if code, body, _ := m.do("POST", "/acme/__admin/sessions/key-based", dana, "{}"); code != 201 {
			t.Fatalf("create: %d %s", code, body)
		}
	}
	m.do("GET", "/acme/hello", "", "")

	if code, _, _ := m.do("POST", "/acme/__admin/org/purge", dana, ""); code != 403 {
		t.Errorf("a member must not purge: %d", code)
	}
	if code, _, _ := m.do("POST", "/acme/__admin/org/purge", authtest.NewKey(m.app.Keys(), "acme", "k", "sessions"), ""); code != 403 {
		t.Errorf("a key must not purge: %d", code)
	}
	var made, refused int
	for _, e := range m.app.Audit().List(t.Context(), "acme") {
		switch {
		case e.Subject == "dana" && e.Status == 201:
			made++
			if e.Issuer != "management-app" || e.TokenID == "" {
				t.Errorf("entry = %+v", e)
			}
		case e.Status == 403:
			refused++
		}
	}
	if made != 2 || refused != 2 {
		t.Errorf("audit: %d creations and %d refusals, want 2 and 2", made, refused)
	}

	code, body, _ := m.do("POST", "/acme/__admin/people/dana/anonymise", system, "")
	if code != 200 || !strings.Contains(body, `"sessions":2`) || !strings.Contains(body, `"auditEntries":3`) {
		t.Errorf("anonymise: %d %s", code, body)
	}
	for _, e := range m.app.Audit().List(t.Context(), "acme") {
		if e.Subject == "dana" {
			t.Errorf("an entry still names dana: %+v", e)
		}
	}
	_, list, _ := m.do("GET", "/acme/__admin/sessions", dana, "")
	if strings.Contains(list, `"createdBy":"dana"`) {
		t.Errorf("sessions still name dana: %s", list)
	}

	_ = m.app.Directory().MarkDeleted("acme")
	code, body, _ = m.do("POST", "/acme/__admin/org/purge", system, "")
	if code != 202 || !strings.Contains(body, `"sessions":2`) {
		t.Errorf("purge: %d %s", code, body)
	}
	if code, _, _ = m.do("POST", "/acme/__admin/org/purge", system, ""); code != 200 {
		t.Errorf("purging again: %d", code)
	}
	if code, _, _ = m.do("GET", "/acme/hello", "", ""); code != 410 {
		t.Errorf("a purged org: %d", code)
	}
}

func TestMetricsNeedASystemToken(t *testing.T) {
	t.Parallel()
	m := newManaged(t, nil)
	m.org("acme")
	if code, _, _ := m.do("GET", "/__metrics", "", ""); code != 401 {
		t.Errorf("without a credential: %d", code)
	}
	if code, _, _ := m.do("GET", "/__metrics", m.issuer.TokenFor("alice", "acme", "admin", nil), ""); code != 403 {
		t.Errorf("with an org's token: %d", code)
	}
	system := m.issuer.TokenFor("management-app", "acme", "admin", authtest.Claims{"scope": "system"})
	if code, _, _ := m.do("GET", "/__metrics", system, ""); code != 200 {
		t.Errorf("with a system token: %d", code)
	}
}

func TestManagedModeServesNoPages(t *testing.T) {
	t.Parallel()
	m := newManaged(t, nil)
	m.org("acme")
	tok := m.issuer.TokenFor("alice", "acme", "admin", nil)
	for _, p := range []string{"/acme/__admin/docs", "/acme/__admin/docs/swagger", "/acme/__admin/swagger-ui/", "/acme/__admin/swagger-ui"} {
		if code, _, _ := m.do("GET", p, tok, ""); code != 404 {
			t.Errorf("GET %s: %d", p, code)
		}
	}
	if code, _, _ := m.do("POST", "/acme/__admin/shutdown", tok, ""); code != 403 && code != 404 {
		t.Errorf("shutdown: %d", code)
	}
	var v struct {
		SchemaVersion int `json:"schemaVersion"`
		MappingSchema int `json:"mappingSchema"`
	}
	_, body, _ := m.do("GET", "/acme/__admin/version", tok, "")
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.SchemaVersion == 0 || v.MappingSchema == 0 {
		t.Errorf("version = %s", body)
	}
}

func TestManagedModeStartsOnlyWhenSafe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*config.Config)
		want   string
	}{
		{"no issuers", func(c *config.Config) { c.Auth.Issuers = nil }, "auth.issuers"},
		{"global writes allowed", func(c *config.Config) { c.Admin.GlobalWrites = "allow" }, "admin.globalWrites"},
		{"unknown fields rejected", func(c *config.Config) { c.Mappings.UnknownFields = "reject" }, "mappings.unknownFields"},
		{"private egress allowed", func(c *config.Config) { c.Egress.BlockPrivate = false }, "egress.blockPrivate"},
		{"browser proxy", func(c *config.Config) { c.Proxy.Browser.Enabled = true }, "proxy.browser"},
		{"a TLS listener", func(c *config.Config) { c.TLS.Port = 8443 }, "tls.port"},
		{"a console", func(c *config.Config) { c.Console.Enabled = true }, "console"},
		{"in-memory stores on many instances", func(c *config.Config) { c.Managed.SingleInstance = false }, "storage.counters"},
		{"jwt helper without keys", func(c *config.Config) { c.Templating.Extensions = []string{"jwt"} }, "templating.jwt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.Mode = "managed"
			cfg.Managed.SingleInstance = true
			cfg.Admin.GlobalWrites, cfg.Mappings.UnknownFields, cfg.Egress.BlockPrivate = "deny", "keep", true
			issuer := authtest.NewIssuer("m", "https://i.example", "aud", clock.Real{})
			cfg.Auth.Issuers = []config.IssuerConfig{{Name: "m", Issuer: issuer.URL, Audience: "aud", PublicKey: issuer.PublicPEM()}}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("the base configuration should be valid: %v", err)
			}
			tt.change(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("want an error mentioning %q, got %v", tt.want, err)
			}
		})
	}
}

// newApp builds an engine from a configuration and returns the error, for tests of a start that should fail.
func newApp(t *testing.T, cfg config.Config) (*app.App, error) {
	t.Helper()
	a, err := app.New(cfg, app.Options{Logger: slog.New(slog.DiscardHandler), Clock: clock.Real{}, Version: "test"})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, nil
}

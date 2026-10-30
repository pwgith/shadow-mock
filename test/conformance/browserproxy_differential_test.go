package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
)

// wiremockProxyURLVar names the variable that points the browser-proxy comparison at a real WireMock
// started with --enable-browser-proxying and --trust-all-proxy-targets (scripts/wiremock-reference.sh
// starts one).
const wiremockProxyURLVar = "SHADOWMOCK_WIREMOCK_PROXY_URL"

// proxiedEnv is a server used as a proxy server: admin calls go to it directly, and the client sends
// everything else through it. Its certificates are the server's own, so the client does not check them.
func proxiedEnv(t *testing.T, base string) (admin, viaProxy *env) {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(tr.CloseIdleConnections)
	c := &http.Client{
		Timeout:       30 * time.Second,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &env{t: t, base: strings.TrimRight(base, "/"), client: tlsClient(false)}, &env{t: t, base: "", client: c}
}

// shadowProxy starts Shadow Mock with browser proxying on, trusting every origin as the WireMock it is
// compared with does.
func shadowProxy(t *testing.T) string {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Storage.Global.Watch = false
	cfg.Proxy.Browser = config.BrowserProxy{Enabled: true, TrustAll: true, CADir: t.TempDir()}
	a, err := app.New(cfg, app.Options{Logger: slog.New(slog.DiscardHandler), Clock: clock.NewFake(epoch), Version: "conformance"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err = a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestBrowserProxyMatchesWireMock sends the same requests through Shadow Mock and through a real
// WireMock used as a proxy server, and compares the answers and what the journal says of them.
func TestBrowserProxyMatchesWireMock(t *testing.T) {
	theirURL := os.Getenv(wiremockProxyURLVar)
	if theirURL == "" {
		t.Skipf("set %s to a WireMock started with --enable-browser-proxying --trust-all-proxy-targets to compare against it", wiremockProxyURLVar)
	}
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Origin", "yes")
		_, _ = io.WriteString(w, r.Method+" "+r.Host+r.URL.RequestURI()+" "+string(b))
	})
	plainOrigin, tlsOrigin := httptest.NewServer(echo), httptest.NewTLSServer(echo)
	t.Cleanup(plainOrigin.Close)
	t.Cleanup(tlsOrigin.Close)

	oAdmin, oVia := proxiedEnv(t, shadowProxy(t))
	wAdmin, wVia := proxiedEnv(t, theirURL)
	t.Cleanup(func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, wAdmin.base+"/__admin/reset", http.NoBody)
		if err != nil {
			t.Error(err)
			return
		}
		if resp, err := wAdmin.client.Do(req); err == nil {
			resp.Body.Close()
		}
	})
	stub := `{"request":{"urlPath":"/bp/stubbed"},"response":{"status":201,"body":"stubbed","headers":{"X-Stub":"1"}}}`
	for _, e := range []*env{oAdmin, wAdmin} {
		if r, _ := e.with(t).admin("POST", "/mappings", stub); r.status != http.StatusCreated {
			t.Fatalf("create mapping: %d %s", r.status, r.body)
		}
		e.with(t).admin("DELETE", "/requests", "")
	}

	cases := []struct {
		name, method, target, body string
		wantStatus                 int
	}{
		{"a stub answers a request to any host over HTTP", "GET", "http://nowhere.invalid/bp/stubbed", "", 201},
		{"a stub answers a request to any host over HTTPS", "GET", "https://nowhere.invalid/bp/stubbed", "", 201},
		{"an unmatched request goes to its origin over HTTP", "GET", plainOrigin.URL + "/bp/origin?a=1", "", 200},
		{"an unmatched request goes to its origin over HTTPS", "GET", tlsOrigin.URL + "/bp/origin?a=1", "", 200},
		{"a body is passed on", "POST", plainOrigin.URL + "/bp/post", "payload", 200},
		{"a body is passed on over HTTPS", "PUT", tlsOrigin.URL + "/bp/put", "payload", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := oVia.with(t).mustSend(call{method: c.method, target: c.target, body: c.body})
			b := wVia.with(t).mustSend(call{method: c.method, target: c.target, body: c.body})
			if a.status != c.wantStatus || b.status != c.wantStatus {
				t.Errorf("status: Shadow Mock %d, WireMock %d, want %d", a.status, b.status, c.wantStatus)
			}
			if !bytes.Equal(a.body, b.body) {
				t.Errorf("body: Shadow Mock %q, WireMock %q", a.body, b.body)
			}
			for _, h := range []string{"X-Origin", "X-Stub"} {
				if a.header.Get(h) != b.header.Get(h) {
					t.Errorf("%s: Shadow Mock %q, WireMock %q", h, a.header.Get(h), b.header.Get(h))
				}
			}
		})
	}
	t.Run("an origin that cannot be reached is a 500 that says so", func(t *testing.T) {
		a := oVia.with(t).mustSend(call{method: "GET", target: "http://nowhere.invalid/bp/down"})
		b := wVia.with(t).mustSend(call{method: "GET", target: "http://nowhere.invalid/bp/down"})
		if a.status != 500 || b.status != 500 {
			t.Errorf("status: Shadow Mock %d, WireMock %d, want 500", a.status, b.status)
		}
		const prefix = "Network failure trying to make a proxied request from "
		if !strings.HasPrefix(string(a.body), prefix) || !strings.HasPrefix(string(b.body), prefix) {
			t.Errorf("body: Shadow Mock %q, WireMock %q", a.body, b.body)
		}
	})
	t.Run("the journal marks proxy requests and counts a pass-through as matched", func(t *testing.T) {
		for _, path := range []string{"/bp/stubbed", "/bp/origin", "/bp/post", "/bp/down"} {
			a, b := proxyJournal(t, oAdmin, path), proxyJournal(t, wAdmin, path)
			if a != b {
				t.Errorf("%s: Shadow Mock %q, WireMock %q", path, a, b)
			}
		}
	})
}

// proxyJournal summarises the journal entries for a path: how each was sent and whether it matched.
func proxyJournal(t *testing.T, e *env, path string) string {
	t.Helper()
	_, v := e.with(t).admin("GET", "/requests", "")
	entries, _ := v["requests"].([]any)
	var out []string
	for _, x := range entries {
		entry, _ := x.(map[string]any)
		r, _ := entry["request"].(map[string]any)
		if !strings.HasPrefix(textOf(r["url"]), path) {
			continue
		}
		matched := "unmatched"
		if m, _ := entry["wasMatched"].(bool); m {
			matched = "matched"
		}
		proxy, _ := r["browserProxyRequest"].(bool)
		port, _ := r["port"].(float64)
		host := textOf(r["host"])
		if host == "127.0.0.1" {
			port = 0 // the origins are on ports of their own
		}
		out = append(out, strings.Join([]string{textOf(r["method"]), textOf(r["scheme"]), host, strconv.Itoa(int(port)), matched, boolText(proxy)}, " "))
	}
	return strings.Join(out, "; ")
}

func boolText(b bool) string {
	if b {
		return "proxy"
	}
	return "direct"
}

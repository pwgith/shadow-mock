package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
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
	"github.com/pwgith/shadow-mock/internal/tlsconf"
)

// wiremockTLSURLVars name the variables that point the TLS comparison at a real WireMock started
// with --https-port and --admin-api-require-https (scripts/wiremock-reference.sh starts one): its
// plain address and its HTTPS address.
const (
	wiremockPlainTLSURLVar = "SHADOWMOCK_WIREMOCK_TLS_PLAIN_URL"
	wiremockTLSURLVar      = "SHADOWMOCK_WIREMOCK_TLS_URL"
)

// tlsEnvs returns Shadow Mock on a plain and on a TLS test server, both over the same engine and
// both able to speak HTTP/2, the way the command serves it with a tls.port.
func tlsEnvs(t *testing.T) (plain, secure *env, securePort int) {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Storage.Global.Watch = false
	cfg.TLS = config.TLS{Port: 1, AdminRequireHTTPS: true}
	a, err := app.New(cfg, app.Options{Logger: slog.New(slog.DiscardHandler), Clock: clock.NewFake(epoch), Version: "conformance"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err = a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	plainSrv := httptest.NewServer(a.Handler())
	t.Cleanup(plainSrv.Close)
	tc, err := tlsconf.Config(cfg.TLS, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	secureSrv := httptest.NewUnstartedServer(a.Handler())
	secureSrv.EnableHTTP2 = true
	secureSrv.TLS = tc
	secureSrv.StartTLS()
	t.Cleanup(secureSrv.Close)
	port := portOf(secureSrv.URL)
	return &env{t: t, base: plainSrv.URL, client: tlsClient(false)}, &env{t: t, base: secureSrv.URL, client: tlsClient(true)}, port
}

// tlsClient trusts any certificate, and speaks HTTP/2 when asked to.
func tlsClient(h2 bool) *http.Client {
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetHTTP2(h2)
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DisableCompression: true, DisableKeepAlives: true, Protocols: &p,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// TestTLSMatchesWireMock sends the same requests over HTTP and HTTPS to Shadow Mock and a real
// WireMock that has an HTTPS listener and requires HTTPS for its admin API, and compares what
// the admin API says about the connection and the answers to a mapping that wants https.
func TestTLSMatchesWireMock(t *testing.T) {
	plainURL, secureURL := os.Getenv(wiremockPlainTLSURLVar), os.Getenv(wiremockTLSURLVar)
	if plainURL == "" || secureURL == "" {
		t.Skipf("set %s and %s to a WireMock with an HTTPS listener to compare against it", wiremockPlainTLSURLVar, wiremockTLSURLVar)
	}
	oPlain, oSecure, oPort := tlsEnvs(t)
	wPlain := &env{t: t, base: strings.TrimRight(plainURL, "/"), client: tlsClient(false)}
	wSecure := &env{t: t, base: strings.TrimRight(secureURL, "/"), client: tlsClient(true)}
	wPort := portOf(wSecure.base)
	t.Cleanup(func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, wSecure.base+"/__admin/reset", http.NoBody)
		if err != nil {
			t.Error(err)
			return
		}
		if resp, err := wSecure.client.Do(req); err == nil {
			resp.Body.Close()
		}
	})

	t.Run("the admin API refuses plain HTTP", func(t *testing.T) {
		a, av := oPlain.with(t).admin("GET", "/mappings", "")
		b, bv := wPlain.with(t).admin("GET", "/mappings", "")
		if a.status != b.status {
			t.Fatalf("status: Shadow Mock %d, WireMock %d", a.status, b.status)
		}
		x, _ := json.Marshal(av)
		y, _ := json.Marshal(bv)
		if !bytes.Equal(x, y) {
			t.Errorf("body: Shadow Mock %s, WireMock %s", x, y)
		}
	})
	t.Run("the admin API answers over HTTPS", func(t *testing.T) {
		if a, b := oSecure.with(t).mustSend(call{method: "GET", target: "/__admin/mappings"}), wSecure.with(t).mustSend(call{method: "GET", target: "/__admin/mappings"}); a.status != 200 || b.status != 200 {
			t.Errorf("status: Shadow Mock %d, WireMock %d", a.status, b.status)
		}
	})

	stub := func(port int) string {
		b, _ := json.Marshal(map[string]any{
			"request":  map[string]any{"urlPath": "/tls/only", "scheme": "https", "port": port},
			"response": map[string]any{"status": 200, "body": "secure"},
		})
		return string(b)
	}
	for e, port := range map[*env]int{oSecure: oPort, wSecure: wPort} {
		if r, _ := e.with(t).admin("POST", "/mappings", stub(port)); r.status != http.StatusCreated {
			t.Fatalf("create mapping: %d %s", r.status, r.body)
		}
	}
	t.Run("scheme and port matching", func(t *testing.T) {
		for _, c := range []struct {
			name           string
			ours, theirs   *env
			wantStatusCode int
		}{
			{"over HTTPS", oSecure, wSecure, 200},
			{"over plain HTTP", oPlain, wPlain, 404},
		} {
			a := c.ours.with(t).mustSend(call{method: "GET", target: "/tls/only"})
			b := c.theirs.with(t).mustSend(call{method: "GET", target: "/tls/only"})
			if a.status != c.wantStatusCode || b.status != c.wantStatusCode {
				t.Errorf("%s: Shadow Mock %d, WireMock %d, want %d", c.name, a.status, b.status, c.wantStatusCode)
			}
		}
	})
	t.Run("the journal names the protocol and scheme", func(t *testing.T) {
		for _, e := range []*env{oSecure, wSecure} {
			e.with(t).admin("DELETE", "/requests", "")
		}
		traffic := []struct {
			ours, theirs *env
			path         string
		}{
			{oPlain, wPlain, "/j/plain"},
			{&env{t: t, base: oSecure.base, client: tlsClient(false)}, &env{t: t, base: wSecure.base, client: tlsClient(false)}, "/j/secure"},
		}
		for _, c := range traffic {
			c.ours.with(t).mustSend(call{method: "GET", target: c.path})
			c.theirs.with(t).mustSend(call{method: "GET", target: c.path})
		}
		// HTTP/2 needs a client that offers it.
		h2o := &env{t: t, base: oSecure.base, client: tlsClient(true)}
		h2w := &env{t: t, base: wSecure.base, client: tlsClient(true)}
		h2o.with(t).mustSend(call{method: "GET", target: "/j/h2"})
		h2w.with(t).mustSend(call{method: "GET", target: "/j/h2"})
		for path, want := range map[string]string{"/j/plain": "HTTP/1.1 http", "/j/secure": "HTTP/1.1 https", "/j/h2": "HTTP/2.0 https"} {
			a, b := loggedFields(t, oSecure, path), loggedFields(t, wSecure, path)
			if a != want || b != want {
				t.Errorf("%s: Shadow Mock %q, WireMock %q, want %q", path, a, b, want)
			}
		}
	})
}

// loggedFields reads what the journal recorded about the connection of the request for path.
func loggedFields(t *testing.T, e *env, path string) string {
	t.Helper()
	_, v := e.with(t).admin("POST", "/requests/find", `{"urlPath":"`+path+`"}`)
	reqs, _ := v["requests"].([]any)
	if len(reqs) != 1 {
		return "no single request"
	}
	r, _ := reqs[0].(map[string]any)
	return textOf(r["protocol"]) + " " + textOf(r["scheme"])
}

func textOf(v any) string {
	s, _ := v.(string)
	return s
}

// portOf is the port in the URL of a server.
func portOf(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(u.Port())
	return port
}

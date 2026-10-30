package conformance_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
)

// corpusUpstream is the address the proxying mappings in corpus/wiremock name as their upstream.
const corpusUpstream = "127.0.0.1:8089"

// upstreamEcho answers every request with what an upstream would want to check: the method, the target,
// the body and the headers the proxy mappings add or remove.
func upstreamEcho(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	seen := map[string]string{}
	for _, h := range []string{"X-Proxied-By", "Authorization", "X-Internal-Secret", "Cookie", "X-Region"} {
		seen[h] = r.Header.Get(h)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"method": r.Method, "target": r.URL.RequestURI(), "body": string(body), "headers": seen,
	})
}

// TestProxyingCorpusMatchesWireMock sends the proxying mappings of the corpus (13-proxying) to Shadow Mock and
// to a real WireMock with an upstream behind both, on the port the mappings name. The generic differential
// test skips these cases because it has no upstream.
func TestProxyingCorpusMatchesWireMock(t *testing.T) {
	base := os.Getenv(wiremockURLVar)
	if base == "" {
		t.Skipf("set %s to a WireMock with corpus/wiremock loaded to compare against it", wiremockURLVar)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", corpusUpstream)
	if err != nil {
		t.Skipf("the corpus names an upstream on %s, which is in use: %v", corpusUpstream, err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(upstreamEcho)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	ours := start(t, "wiremock")
	theirs := &env{t: t, base: strings.TrimRight(base, "/"), client: ours.client}

	cases := []struct {
		name string
		want string // the corpus mapping that serves it
		call call
		// network means the upstream cannot be reached, so only the failure is compared.
		network bool
	}{
		{"a request is proxied with its query", "c013000101", get("/wm/proxy/payments/1?x=1"), false},
		{"a body and method are passed on", "c013000101", post("/wm/proxy/payments/2", "payload", jsonCT, appJS), false},
		{"a header is added", "c013000103", get("/wm/proxy/headers/1"), false},
		{"a URL prefix is removed", "c013000104", get("/wm/proxy/api/v1/orders?o=2"), false},
		{"a target chosen by a template that cannot be reached", "c013000105", get("/wm/proxy/regional/x", "X-Region", "eu"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := ours.with(t).mustSend(c.call), theirs.with(t).mustSend(c.call)
			if a.status != b.status {
				t.Fatalf("status: Shadow Mock %d, WireMock %d\nShadow Mock: %s\nWireMock: %s", a.status, b.status, a.body, b.body)
			}
			if c.network {
				const prefix = "Network failure trying to make a proxied request from "
				if a.status != http.StatusInternalServerError || !strings.HasPrefix(string(a.body), prefix) || !strings.HasPrefix(string(b.body), prefix) {
					t.Errorf("want a 500 that says so on both, got %d %q and %q", a.status, a.body, b.body)
				}
				return
			}
			if a.status != http.StatusOK {
				t.Errorf("want the upstream's 200, got %d %s", a.status, a.body)
			}
			if !bytes.Equal(a.body, b.body) {
				t.Errorf("what the upstream saw differs:\nShadow Mock: %s\nWireMock:    %s", a.body, b.body)
			}
		})
	}
}

// TestProxyHeaderRulesDifferFromWireMock pins the two places where proxying differs from WireMock 3.13.2 on purpose
// (docs/COMPATIBILITY.md), so a change on either side is noticed. The corpus mapping c013000103 adds
// Authorization and X-Proxied-By and removes X-Internal-Secret and Cookie; the client sends all three.
func TestProxyHeaderRulesDifferFromWireMock(t *testing.T) {
	base := os.Getenv(wiremockURLVar)
	if base == "" {
		t.Skipf("set %s to a WireMock with corpus/wiremock loaded to compare against it", wiremockURLVar)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", corpusUpstream)
	if err != nil {
		t.Skipf("the corpus names an upstream on %s, which is in use: %v", corpusUpstream, err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(upstreamEcho)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	ours := start(t, "wiremock")
	theirs := &env{t: t, base: strings.TrimRight(base, "/"), client: ours.client}
	c := call{method: "GET", target: "/wm/proxy/headers/1", header: hdr("X-Internal-Secret", "shh", "Cookie", "a=b", "Authorization", "Bearer client")}
	seen := func(e *env) map[string]string {
		var echo struct {
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal(e.with(t).mustSend(c).body, &echo); err != nil {
			t.Fatal(err)
		}
		return echo.Headers
	}
	// Shadow Mock replaces a header it is told to add and removes names whatever their case.
	want := map[string]string{"X-Proxied-By": "shadow-mock-corpus", "Authorization": "Bearer upstream-token", "X-Internal-Secret": "", "Cookie": ""}
	for k, v := range want {
		if got := seen(ours)[k]; got != v {
			t.Errorf("Shadow Mock: %s = %q, want %q", k, got, v)
		}
	}
	// WireMock adds the header beside the client's, and removes only names written in lower case.
	wm := map[string]string{"X-Proxied-By": "shadow-mock-corpus", "Authorization": "Bearer client", "X-Internal-Secret": "shh", "Cookie": "a=b"}
	for k, v := range wm {
		if got := seen(theirs)[k]; got != v {
			t.Errorf("WireMock: %s = %q, want %q (has WireMock changed? update docs/COMPATIBILITY.md)", k, got, v)
		}
	}
}

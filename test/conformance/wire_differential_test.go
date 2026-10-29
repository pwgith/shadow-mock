package conformance_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/config"
)

// wiremockCORSURLVar names the environment variable that points the wire comparison at a real
// WireMock started with --enable-stub-cors (scripts/wiremock-reference.sh starts one).
const wiremockCORSURLVar = "SHADOWMOCK_WIREMOCK_CORS_URL"

// wireCase is one request sent to both servers, and what must be the same in the replies.
type wireCase struct {
	name string
	call call
	// headers lists the reply headers that must be equal (absent on both counts as equal).
	headers []string
	// body compares the reply bodies. A body that is gzip is inflated first.
	body bool
	// status compares the reason phrase too.
	reason bool
}

func gzipped(s string) string {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return b.String()
}

// compareWire sends each case to both servers and reports what differs.
func compareWire(t *testing.T, ours, theirs *env, cases []wireCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := ours.with(t).mustSend(c.call), theirs.with(t).mustSend(c.call)
			if a.status != b.status {
				t.Errorf("status: Shadow Mock %d, WireMock %d (%s | %s)", a.status, b.status, abbreviate(string(a.body), 120), abbreviate(string(b.body), 120))
				return
			}
			if c.reason && a.statusText != b.statusText {
				t.Errorf("reason phrase: Shadow Mock %q, WireMock %q", a.statusText, b.statusText)
			}
			for _, h := range c.headers {
				if x, y := a.header.Values(h), b.header.Values(h); strings.Join(x, "|") != strings.Join(y, "|") {
					t.Errorf("header %s: Shadow Mock %v, WireMock %v", h, x, y)
				}
			}
			if c.body && !sameText(inflate(a), inflate(b)) {
				t.Errorf("body: Shadow Mock %q, WireMock %q", abbreviate(string(a.body), 120), abbreviate(string(b.body), 120))
			}
		})
	}
}

func inflate(r *reply) string {
	if r.header.Get("Content-Encoding") != "gzip" {
		return string(r.body)
	}
	zr, err := gzip.NewReader(bytes.NewReader(r.body))
	if err != nil {
		return "<bad gzip>"
	}
	b, _ := io.ReadAll(zr)
	return string(b)
}

// addMapping creates a mapping through the admin API.
func addMapping(e *env, body string) {
	e.t.Helper()
	e.mustSend(call{method: "POST", target: "/__admin/mappings", header: hdr("Content-Type", "application/json"), body: body})
}

// TestWireBehaviourMatchesWireMock compares how replies are framed (FR-10.10) and what the server
// does besides serving mappings (FR-10.11) with a real WireMock.
func TestWireBehaviourMatchesWireMock(t *testing.T) {
	base := os.Getenv(wiremockURLVar)
	if base == "" {
		t.Skipf("set %s to a WireMock to compare against it", wiremockURLVar)
	}
	ours := start(t, "wiremock")
	theirs := &env{t: t, base: strings.TrimRight(base, "/"), client: ours.client}
	t.Cleanup(func() {
		for _, c := range []call{{method: "POST", target: "/__admin/mappings/reset"}, {method: "DELETE", target: "/__admin/requests"}} {
			req, err := http.NewRequestWithContext(context.Background(), c.method, theirs.base+c.target, http.NoBody)
			if err != nil {
				t.Error(err)
				continue
			}
			if resp, err := theirs.client.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	})
	for _, e := range []*env{ours.with(t), theirs.with(t)} {
		addMapping(e, `{"id":"11111111-1111-4111-8111-111111111111","name":"sm","request":{"urlPath":"/wire5/sm"},"response":{"status":200,"statusMessage":"All Good","body":"x"}}`)
		addMapping(e, `{"id":"22222222-2222-4222-8222-222222222222","request":{"urlPath":"/wire5/nope"},"response":{"status":404,"statusMessage":"Nope","headers":{"X-A":"b"}}}`)
		addMapping(e, `{"id":"33333333-3333-4333-8333-333333333333","name":"small","request":{"urlPath":"/wire5/small"},"response":{"status":200,"body":"tiny"}}`)
		addMapping(e, `{"id":"44444444-4444-4444-8444-444444444444","request":{"urlPath":"/wire5/big"},"response":{"status":200,"body":"`+strings.Repeat("x", 5000)+`","headers":{"Content-Type":"text/plain"}}}`)
		addMapping(e, `{"id":"55555555-5555-4555-8555-555555555555","name":"echo","request":{"method":"POST","urlPath":"/wire5/echo","bodyPatterns":[{"equalTo":"hello gz"}]},"response":{"status":200,"body":"matched"}}`)
		addMapping(e, `{"id":"66666666-6666-4666-8666-666666666666","request":{"urlPath":"/wire5/proxy"},"response":{"proxyBaseUrl":"http://127.0.0.1:1"}}`)
		addMapping(e, `{"id":"77777777-7777-4777-8777-777777777777","request":{"urlPath":"/text/terms.txt"},"response":{"status":200,"body":"from the mapping"}}`)
	}
	gz := hdr("Accept-Encoding", "gzip")
	t.Run("framing", func(t *testing.T) {
		compareWire(t, ours, theirs, []wireCase{
			{name: "statusMessage", call: get("/wire5/sm"), reason: true, headers: []string{"Matched-Stub-Id", "Matched-Stub-Name"}, body: true},
			{name: "statusMessage on a 404 with a header", call: get("/wire5/nope"), reason: true, headers: []string{"X-A", "Matched-Stub-Id", "Matched-Stub-Name"}},
			{name: "no statusMessage", call: get("/wire5/small"), reason: true, headers: []string{"Matched-Stub-Id", "Matched-Stub-Name", "Content-Encoding"}, body: true},
			{name: "a small body with gzip", call: get("/wire5/small", "Accept-Encoding", "gzip"), headers: []string{"Content-Encoding", "Vary"}, body: true},
			{name: "a big body with gzip", call: call{method: "GET", target: "/wire5/big", header: gz}, headers: []string{"Content-Encoding", "Vary", "Content-Type"}, body: true},
			{name: "gzip refused", call: get("/wire5/big", "Accept-Encoding", "gzip;q=0"), headers: []string{"Content-Encoding"}, body: true},
			{name: "a request body sent with gzip", call: post("/wire5/echo", gzipped("hello gz"), "Content-Encoding", "gzip", "Content-Type", "text/plain"), headers: []string{"Matched-Stub-Name"}, body: true},
			{name: "a request body sent as text", call: post("/wire5/echo", "hello gz", "Content-Type", "text/plain"), body: true},
			{name: "a request nothing matched", call: get("/wire5/missing"), headers: []string{"Matched-Stub-Id"}},
			{name: "a proxy target that is down", call: get("/wire5/proxy")},
		})
	})
	t.Run("proxy failure wording", func(t *testing.T) {
		a, b := ours.with(t).mustSend(get("/wire5/proxy")), theirs.with(t).mustSend(get("/wire5/proxy"))
		const lead = "Network failure trying to make a proxied request from "
		if !strings.HasPrefix(string(a.body), lead) || !strings.HasPrefix(string(b.body), lead) {
			t.Errorf("bodies: Shadow Mock %q, WireMock %q; both should begin %q", a.body, b.body, lead)
		}
	})
	t.Run("files", func(t *testing.T) {
		compareWire(t, ours, theirs, []wireCase{
			{name: "under /__files/", call: get("/__files/text/terms.txt"), headers: []string{"Content-Type"}, body: true},
			{name: "at the file's own path", call: get("/text/terms.txt"), headers: []string{"Content-Type"}, body: true},
			{name: "a mapping wins at the own path", call: get("/wire5/sm")},
			{name: "a missing file", call: get("/__files/text/none.txt")},
			{name: "a folder", call: get("/__files/text/")},
			{name: "a path that climbs out", call: get("/__files/../mappings/x.json")},
			{name: "an encoded path that climbs out", call: get("/__files/%2e%2e/mappings/x.json")},
			{name: "an unknown path", call: get("/nothing/here")},
		})
	})

	t.Run("stub CORS", func(t *testing.T) {
		corsBase := os.Getenv(wiremockCORSURLVar)
		if corsBase == "" {
			t.Skipf("set %s to a WireMock started with --enable-stub-cors", wiremockCORSURLVar)
		}
		o := startWith(t, "wiremock", func(c *config.Config) { c.Server.StubCORS = true })
		w := &env{t: t, base: strings.TrimRight(corsBase, "/"), client: o.client}
		for _, e := range []*env{o.with(t), w.with(t)} {
			addMapping(e, `{"request":{"urlPath":"/wire5/cors"},"response":{"status":200,"body":"c"}}`)
			addMapping(e, `{"request":{"urlPath":"/wire5/own"},"response":{"status":200,"body":"o","headers":{"Access-Control-Allow-Origin":"https://mine.example"}}}`)
		}
		t.Cleanup(func() {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, w.base+"/__admin/mappings/reset", http.NoBody)
			if err == nil {
				if resp, err := w.client.Do(req); err == nil {
					resp.Body.Close()
				}
			}
		})
		corsHeaders := []string{
			"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Methods",
			"Access-Control-Allow-Headers", "Access-Control-Max-Age", "Vary",
		}
		compareWire(t, o, w, []wireCase{
			{name: "preflight", call: call{method: "OPTIONS", target: "/wire5/cors", header: hdr("Origin", "https://a.example", "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "X-Custom, Content-Type")}, headers: corsHeaders, body: true},
			{name: "preflight with nothing to ask for", call: call{method: "OPTIONS", target: "/wire5/cors", header: hdr("Origin", "https://a.example", "Access-Control-Request-Method", "GET")}, headers: corsHeaders},
			{name: "preflight for a path with no mapping", call: call{method: "OPTIONS", target: "/wire5/none", header: hdr("Origin", "https://a.example", "Access-Control-Request-Method", "GET")}, headers: corsHeaders},
			{name: "a request with an origin", call: call{method: "GET", target: "/wire5/cors", header: hdr("Origin", "https://a.example")}, headers: corsHeaders, body: true},
			{name: "a request with no origin", call: get("/wire5/cors"), headers: corsHeaders, body: true},
			{name: "an options request that is not a preflight", call: call{method: "OPTIONS", target: "/wire5/cors", header: hdr("Origin", "https://a.example")}, headers: corsHeaders},
			// WireMock sends its own Access-Control-Allow-Origin and then the mapping's; Shadow Mock sends the mapping's alone.
			{name: "a mapping that sets its own header", call: call{method: "GET", target: "/wire5/own", header: hdr("Origin", "https://a.example")}, headers: []string{"Access-Control-Allow-Credentials", "Vary"}},
			{name: "the origin null", call: call{method: "GET", target: "/wire5/cors", header: hdr("Origin", "null")}, headers: corsHeaders},
			{name: "a file", call: call{method: "GET", target: "/__files/text/terms.txt", header: hdr("Origin", "https://a.example")}, headers: corsHeaders},
		})
	})
}

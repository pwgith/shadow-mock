package conformance_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/test/contract"
)

// callRuleFor returns the response definition for call n by evaluating the mapping's own
// callResponses, written out again here so the test does not rely on the engine's logic:
// the first rule whose selector matches wins, otherwise the default response.
func callRuleFor(m mapping, n int) map[string]any {
	for _, r := range m.CallResponses {
		resp, _ := r["response"].(map[string]any)
		if calls, ok := r["calls"].(string); ok && callsMatch(calls, n) {
			return resp
		}
		if every, ok := r["every"].(float64); ok && n%int(every) == 0 {
			return resp
		}
	}
	return m.Response
}

// callsMatch evaluates "3", "2-4" and "6+".
func callsMatch(sel string, n int) bool {
	switch {
	case strings.HasSuffix(sel, "+"):
		from, _ := strconv.Atoi(strings.TrimSuffix(sel, "+"))
		return n >= from
	case strings.Contains(sel, "-"):
		a, b, _ := strings.Cut(sel, "-")
		from, _ := strconv.Atoi(a)
		to, _ := strconv.Atoi(b)
		return n >= from && n <= to
	default:
		only, _ := strconv.Atoi(sel)
		return n == only
	}
}

// requestFor builds a request that matches a mapping with only a method, a path and equalTo
// headers, which is all the call-count mappings use.
func requestFor(m mapping) call {
	c := call{method: fmt.Sprint(m.Request["method"]), target: fmt.Sprint(m.Request["urlPath"]), header: map[string][]string{}}
	if hs, ok := m.Request["headers"].(map[string]any); ok {
		for k, v := range hs {
			if eq, ok := v.(map[string]any); ok {
				c.header[k] = []string{fmt.Sprint(eq["equalTo"])}
			}
		}
	}
	return c
}

func TestCallCountResponses(t *testing.T) {
	// The subtests share one engine and its journal, so they run in order.
	e := start(t, "shadow-mock")
	ms := loadMappings(t, "shadow-mock")
	const calls = 12

	tested := 0
	for _, m := range ms {
		if len(m.CallResponses) == 0 || !strings.HasPrefix(m.ID, "5d01") {
			continue
		}
		tested++
		t.Run(fmt.Sprintf("%s %s", m.ID[:8]+m.ID[len(m.ID)-2:], m.Request["urlPath"]), func(t *testing.T) {
			se := e.with(t)
			req := requestFor(m)
			if _, hasHeader := m.Request["headers"]; hasHeader {
				// Requests the mapping does not serve are not counted.
				se.mustSend(call{method: req.method, target: req.target})
				if _, matched := se.latest(); matched {
					t.Fatal("a request without the required header was served")
				}
			}
			for n := 1; n <= calls; n++ {
				def := callRuleFor(m, n)
				r, err := se.send(req)
				if def["fault"] != nil {
					if err == nil {
						t.Errorf("call %d: expected a fault, got status %d", n, r.status)
					}
					continue
				}
				if err != nil {
					t.Fatalf("call %d: %v", n, err)
				}
				if id, _ := se.latest(); id != m.ID {
					t.Fatalf("call %d served by %s, want %s", n, id, m.ID)
				}
				se.expectResponse(fmt.Sprintf("call %d", n), def, r, 0)
			}
			_, list := se.admin("GET", "/counters", "")
			if got := counterOf(list, m.ID); got != calls {
				t.Errorf("counter = %d after %d served calls, want %d", got, calls, calls)
			}
		})
	}
	if tested != 10 {
		t.Errorf("tested %d call-count mappings, the corpus has 10 in folder 01", tested)
	}

	t.Run("a counter can be set and reset", func(t *testing.T) {
		se := e.with(t)
		tenth, ok := find(ms, "5d01000111")
		if !ok {
			t.Fatal("corpus mapping 5d01000111 is missing")
		}
		se.admin("POST", "/counters/reset", "")
		body, err := os.ReadFile(filepath.Join(contract.RepoRoot(), "corpus", "shadow-mock", "sessions", "counters", "set-counter.json"))
		if err != nil {
			t.Fatal(err)
		}
		if r, _ := se.admin("PUT", "/counters/"+tenth.ID, string(body)); r.status != 200 {
			t.Fatalf("set counter: %d %s", r.status, r.body)
		}
		// Setting the count to 9 makes the next call the tenth.
		se.expectResponse("tenth call", callRuleFor(tenth, 10), se.mustSend(requestFor(tenth)), 0)
		se.admin("POST", "/counters/reset", "")
		se.expectResponse("first call after reset", callRuleFor(tenth, 1), se.mustSend(requestFor(tenth)), 0)
	})
}

func counterOf(list map[string]any, id string) int {
	cs, _ := list["counters"].([]any)
	for _, c := range cs {
		cm, _ := c.(map[string]any)
		if cm["stubMappingId"] == id {
			n, _ := cm["count"].(float64)
			return int(n)
		}
	}
	return 0
}

func TestGzipResponses(t *testing.T) {
	t.Parallel()
	e := start(t, "shadow-mock")
	ms := loadMappings(t, "shadow-mock")
	gz := map[string][]string{"Accept-Encoding": {"gzip"}}
	byShort := func(short string) mapping {
		m, ok := find(ms, short)
		if !ok {
			t.Fatalf("corpus mapping %s is missing", short)
		}
		return m
	}

	t.Run("a large response is compressed for a client that accepts gzip", func(t *testing.T) {
		t.Parallel()
		se, m := e.with(t), byShort("5d02000101")
		r := se.mustSend(call{method: "GET", target: "/sm/gzip/claims", header: gz})
		if r.header.Get("Content-Encoding") != "gzip" || r.header.Get("Vary") != "" {
			t.Errorf("headers = %v, want Content-Encoding gzip and no Vary", r.header)
		}
		se.expectResponse("claims", m.Response, r, 0) // inflates the body, then compares with the file
	})
	t.Run("it is not compressed for a client that does not accept gzip", func(t *testing.T) {
		t.Parallel()
		se, m := e.with(t), byShort("5d02000101")
		r := se.mustSend(call{method: "GET", target: "/sm/gzip/claims"})
		if r.header.Get("Content-Encoding") != "" {
			t.Errorf("Content-Encoding = %q, want none", r.header.Get("Content-Encoding"))
		}
		se.expectResponse("claims", m.Response, r, 0)
	})
	t.Run("gzipDisabled keeps a large response uncompressed", func(t *testing.T) {
		t.Parallel()
		se, m := e.with(t), byShort("5d02000102")
		r := se.mustSend(call{method: "GET", target: "/sm/gzip/claims-uncompressed", header: gz})
		if r.header.Get("Content-Encoding") != "" {
			t.Errorf("Content-Encoding = %q, want none", r.header.Get("Content-Encoding"))
		}
		se.expectResponse("claims-uncompressed", m.Response, r, 0)
	})
	t.Run("a small response is compressed too", func(t *testing.T) {
		t.Parallel()
		se, m := e.with(t), byShort("5d02000103")
		r := se.mustSend(call{method: "GET", target: "/sm/gzip/tiny", header: gz})
		if r.header.Get("Content-Encoding") != "gzip" {
			t.Errorf("Content-Encoding = %q, want gzip", r.header.Get("Content-Encoding"))
		}
		se.expectResponse("tiny", m.Response, r, 0)
	})
	t.Run("an already encoded body is sent as written", func(t *testing.T) {
		t.Parallel()
		se, m := e.with(t), byShort("5d02000104")
		r := se.mustSend(call{method: "GET", target: "/sm/gzip/pre-encoded", header: gz})
		want, _ := base64.StdEncoding.DecodeString(fmt.Sprint(m.Response["base64Body"]))
		if r.header.Get("Content-Encoding") != "gzip" || !bytes.Equal(r.body, want) {
			t.Errorf("Content-Encoding = %q, body %x; want the stored gzip bytes %x", r.header.Get("Content-Encoding"), r.body, want)
		}
	})
}

// WireMock has no number matchers, so they live in the Shadow Mock tree and are not part of the
// WireMock corpus that is replayed against a real WireMock.
func numberMatcherCases() []tc {
	j := func(path, body string) call { return post(path, body, jsonCT, appJS) }
	return []tc{
		{want: "5d04000101", call: get("/wm/numbers/equal?qty=5.00")},
		{want: "5d04000102", call: get("/wm/numbers/large-order?amount=1500")},
		{want: "5d04000103", call: get("/wm/numbers/adult", "X-Age", "18")},
		{want: "5d04000104", call: get("/wm/numbers/low-stock?stock=3")},
		{want: "5d04000105", call: get("/wm/numbers/band?score=60")},
		{call: get("/wm/numbers/band?score=50")},
		{want: "5d04000201", call: j("/wm/jsonpath/amount", `{ "amount": 1500.75 }`)},
		{call: j("/wm/jsonpath/amount", `{ "amount": 900 }`)},
	}
}

func TestNumberMatchers(t *testing.T) {
	e := start(t, "shadow-mock")
	ms := loadMappings(t, "shadow-mock")
	for _, c := range numberMatcherCases() {
		name := c.call.method + " " + c.call.target
		t.Run(name, func(t *testing.T) { runCase(t, e.with(t), ms, c) })
	}
}

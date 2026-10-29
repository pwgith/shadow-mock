package conformance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/test/contract"
)

// wiremockURLVar names the environment variable that points the differential test at a real
// WireMock that has corpus/wiremock loaded as its root (scripts/wiremock-reference.sh starts one).
const wiremockURLVar = "SHADOWMOCK_WIREMOCK_URL"

// differentialSkip lists corpus cases the two servers cannot be compared on, with the reason.
// They are about the environment (clock, address, upstreams), never about behaviour.
var differentialSkip = map[string]string{
	"c003000403": "scheme and port matching needs an HTTPS listener on 8443",
	"c004000302": "relative dates are measured from each server's own clock; the corpus requests assume Shadow Mock's fixed clock",
	"c004000303": "relative dates are measured from each server's own clock; the corpus requests assume Shadow Mock's fixed clock",
	"c004000306": "truncates to the current month, which depends on each server's own clock",
	"c010000106": "the body holds a generated id and the current time",
	"c010000101": "the body holds the request id and the server's own address",
	"c010000201": "the body holds the current time",
	"c010000202": "the body holds random values",
	"c011000106": "waits 10 s on purpose",
	"c013000101": "proxies to an upstream that only the acceptance suite starts",
	"c013000103": "proxies to an upstream that only the acceptance suite starts",
	"c013000104": "proxies to an upstream that only the acceptance suite starts",
	"c013000105": "proxies to an upstream that only the acceptance suite starts",
}

// knownDifferences lists corpus cases where Shadow Mock deliberately answers differently from
// WireMock, with the reason. A difference here is logged, not failed; every entry must also be in
// docs/COMPATIBILITY.md. An unexplained difference fails the test.
var knownDifferences = map[string]string{
	"c012000103": "WireMock's malformed-chunk fault does not make Go's HTTP client fail, while Shadow Mock's does; both are broken responses",
	"c009000205": "a missing body file is a 500 on both; WireMock's body is an HTML stack trace, Shadow Mock's is the JSON error shape",
}

// ignoredHeaders are headers one server adds that say nothing about behaviour.
var ignoredHeaders = []string{
	"Date", "Server", "Transfer-Encoding", "Content-Length", "Connection", "Keep-Alive", "Vary",
}

// TestDifferentialAgainstRealWireMock replays every corpus request against Shadow Mock and a
// real WireMock and compares the replies and the request journals. The expected results in the
// other conformance tests come from this engine; this is the test whose expectations come from
// WireMock itself.
func TestDifferentialAgainstRealWireMock(t *testing.T) {
	base := os.Getenv(wiremockURLVar)
	if base == "" {
		t.Skipf("set %s to a WireMock with corpus/wiremock loaded to compare against it", wiremockURLVar)
	}
	ours := start(t, "wiremock")
	theirs := &env{t: t, base: strings.TrimRight(base, "/"), client: ours.client}

	t.Run("both servers loaded the same mappings", func(t *testing.T) {
		o, w := ours.with(t), theirs.with(t)
		a, b := mappingIDs(o), mappingIDs(w)
		if missing := difference(b, a); len(missing) > 0 {
			t.Errorf("WireMock loaded mappings Shadow Mock did not: %v", missing)
		}
		if extra := difference(a, b); len(extra) > 0 {
			t.Errorf("Shadow Mock loaded mappings WireMock did not (a WireMock that cannot load a corpus file is not a reference): %v", extra)
		}
	})

	for _, c := range wiremockCases() {
		if reason, skip := differentialSkip[c.want]; skip && c.want != "" {
			t.Run(caseName(c)+" (skipped)", func(t *testing.T) { t.Skip(reason) })
			continue
		}
		t.Run(caseName(c), func(t *testing.T) {
			compareCase(t, ours.with(t), theirs.with(t), c)
		})
	}

	t.Run("the journals agree", func(t *testing.T) {
		compareJournals(t, ours.with(t), theirs.with(t))
	})
}

func caseName(c tc) string {
	name := c.call.method + " " + c.call.target
	if len(name) > 90 {
		name = name[:90]
	}
	if c.want != "" {
		name += " → " + c.want
	}
	return name
}

// compareCase sends one case to both servers and compares what came back.
func compareCase(t *testing.T, ours, theirs *env, c tc) {
	t.Helper()
	var problems []string
	note := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	o, oerr := ours.send(c.call)
	w, werr := theirs.send(c.call)
	switch {
	case c.fails:
		if oerr == nil || werr == nil {
			note("a fault should break the connection on both servers; Shadow Mock err=%v, WireMock err=%v", oerr, werr)
		}
	case oerr != nil || werr != nil:
		note("transport error: Shadow Mock %v, WireMock %v", oerr, werr)
	default:
		if o.status != w.status {
			note("status: Shadow Mock %d, WireMock %d", o.status, w.status)
		}
		if c.want != "" { // an unmatched request's diagnostic text is each server's own
			problems = append(problems, headerDifferences(o.header, w.header, ours.base, theirs.base)...)
			if msg := bodyDifference(o, w, ours.base, theirs.base); msg != "" {
				note("%s", msg)
			}
		}
	}
	if len(problems) == 0 {
		return
	}
	report := strings.Join(problems, "\n")
	if reason, known := knownDifferences[c.want]; known && c.want != "" {
		t.Logf("known difference (%s):\n%s", reason, report)
		return
	}
	t.Errorf("Shadow Mock and WireMock differ:\n%s", report)
}

func headerDifferences(ours, theirs http.Header, oursBase, theirsBase string) []string {
	var out []string
	names := map[string]bool{}
	for k := range ours {
		names[http.CanonicalHeaderKey(k)] = true
	}
	for k := range theirs {
		names[http.CanonicalHeaderKey(k)] = true
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if slices.ContainsFunc(ignoredHeaders, func(h string) bool { return strings.EqualFold(h, k) }) {
			continue
		}
		o, w := withoutHost(ours.Values(k), oursBase), withoutHost(theirs.Values(k), theirsBase)
		if !slices.Equal(o, w) {
			out = append(out, fmt.Sprintf("header %s: Shadow Mock %q, WireMock %q", k, o, w))
		}
	}
	return out
}

// bodyDifference compares two bodies, as JSON when both are JSON so layout does not matter.
func bodyDifference(ours, theirs *reply, oursBase, theirsBase string) string {
	ob := bytes.ReplaceAll(ours.body, []byte(strings.TrimPrefix(oursBase, "http://")), []byte("HOST"))
	wb := bytes.ReplaceAll(theirs.body, []byte(strings.TrimPrefix(theirsBase, "http://")), []byte("HOST"))
	if bytes.Equal(ob, wb) || bytes.Equal(bytes.ReplaceAll(wb, []byte(crlf), []byte("\n")), ob) {
		return ""
	}
	var o, w any
	if json.Unmarshal(ob, &o) == nil && json.Unmarshal(wb, &w) == nil && reflect.DeepEqual(o, w) {
		return ""
	}
	return fmt.Sprintf("body (%d bytes from Shadow Mock, %d from WireMock):\nShadow Mock: %.400s\nWireMock:    %.400s", len(ob), len(wb), ob, wb)
}

func mappingIDs(e *env) []string {
	e.t.Helper()
	r, v := e.admin("GET", "/mappings?limit=100000", "")
	if r.status != http.StatusOK {
		e.t.Fatalf("listing mappings on %s: %d %s", e.base, r.status, r.body)
	}
	ms, _ := v["mappings"].([]any)
	var out []string
	for _, m := range ms {
		if id, ok := m.(map[string]any)["id"].(string); ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// difference returns the elements of a that are not in b.
func difference(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// journalEntry is what the journal says about one request, in the fields both servers share.
type journalEntry struct {
	Method, URL, Body, Stub string
	ReqB64, RespB64         string
	HasShape                bool // the request carries bodyAsBase64, as WireMock's does
	Status                  int
	Matched                 bool
	RespBody                string
}

func journalOf(e *env) []journalEntry {
	e.t.Helper()
	r, v := e.admin("GET", "/requests?limit=100000", "")
	if r.status != http.StatusOK {
		e.t.Fatalf("reading the journal on %s: %d %.200s", e.base, r.status, r.body)
	}
	reqs, _ := v["requests"].([]any)
	out := make([]journalEntry, 0, len(reqs))
	for _, x := range reqs {
		m, _ := x.(map[string]any)
		req, _ := m["request"].(map[string]any)
		resp, _ := m["response"].(map[string]any)
		je := journalEntry{Method: str(req["method"]), URL: str(req["url"]), RespBody: strings.ReplaceAll(str(resp["body"]), strings.TrimPrefix(e.base, "http://"), "HOST")}
		je.Body, je.ReqB64 = str(req["body"]), str(req["bodyAsBase64"])
		je.RespB64 = str(resp["bodyAsBase64"])
		_, je.HasShape = req["bodyAsBase64"]
		if s, ok := resp["status"].(float64); ok {
			je.Status = int(s)
		}
		je.Matched, _ = m["wasMatched"].(bool)
		if sm, ok := m["stubMapping"].(map[string]any); ok {
			je.Stub = str(sm["id"])
		}
		out = append(out, je)
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// compareJournals checks that both servers recorded the same requests, matched them to the same
// mappings, and kept the same bodies, whole. Truncated bodies are the kind of difference the
// reply comparison cannot see. Entries are paired by method, URL and body, so one extra or missing
// request is reported as that and does not shift every comparison after it.
func compareJournals(t *testing.T, ours, theirs *env) {
	t.Helper()
	o, w := journalOf(ours), journalOf(theirs)
	key := func(e journalEntry) string { return e.Method + " " + e.URL + " " + e.Body }
	pool := map[string][]journalEntry{}
	for _, e := range w {
		pool[key(e)] = append(pool[key(e)], e)
	}
	for _, e := range o {
		k := key(e)
		match := pool[k]
		if len(match) == 0 {
			if reason, known := knownDifferences[shortOf(e.Stub)]; known {
				t.Logf("known difference (%s): only Shadow Mock journals %s", reason, abbreviate(e.URL, 90))
				continue
			}
			t.Errorf("only Shadow Mock has a journal entry for %s %s", e.Method, abbreviate(e.URL, 90))
			continue
		}
		pool[k] = match[1:]
		if diff := entryDifference(e, match[0]); diff != "" {
			if reason, known := knownDifferences[shortOf(e.Stub)]; known {
				t.Logf("known difference (%s) in the journal entry for %s %s", reason, e.Method, abbreviate(e.URL, 90))
				continue
			}
			t.Errorf("journal entry for %s %s differs: %s", e.Method, abbreviate(e.URL, 90), diff)
		}
	}
	for k, left := range pool {
		for range left {
			t.Errorf("only WireMock has a journal entry for %s", abbreviate(k, 100))
		}
	}
}

// entryDifference describes how two journal entries for the same request differ, or returns "".
func entryDifference(ours, theirs journalEntry) string {
	var out []string
	if ours.Matched != theirs.Matched {
		out = append(out, fmt.Sprintf("matched: Shadow Mock %v, WireMock %v", ours.Matched, theirs.Matched))
	}
	if ours.Matched && shortOf(ours.Stub) != shortOf(theirs.Stub) {
		out = append(out, fmt.Sprintf("served by %s on Shadow Mock, %s on WireMock", shortOf(ours.Stub), shortOf(theirs.Stub)))
	}
	if ours.Status != theirs.Status {
		out = append(out, fmt.Sprintf("status: Shadow Mock %d, WireMock %d", ours.Status, theirs.Status))
	}
	if ours.ReqB64 != theirs.ReqB64 {
		out = append(out, "request bodyAsBase64 differs")
	}
	if !ours.HasShape {
		out = append(out, "the request has no bodyAsBase64")
	}
	if ours.Matched && !sameText(ours.RespBody, theirs.RespBody) {
		out = append(out, fmt.Sprintf("response body: %d bytes on Shadow Mock, %d on WireMock", len(ours.RespBody), len(theirs.RespBody)))
	}
	return strings.Join(out, "; ")
}

const crlf = "\r\n"

// sameText compares two bodies, as JSON when both are JSON so layout does not matter.
func sameText(a, b string) bool {
	// A WireMock on Windows ends lines with CRLF where it formats text; the layout is the same.
	a, b = strings.ReplaceAll(a, crlf, "\n"), strings.ReplaceAll(b, crlf, "\n")
	if a == b {
		return true
	}
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}

func shortOf(id string) string {
	if len(id) < 10 {
		return ""
	}
	return id[:8] + id[len(id)-2:]
}

func abbreviate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// TestLargeBodiesAreReadBackWhole needs no WireMock: it checks that Shadow Mock's journal returns
// a large request and a large response without cutting them, as WireMock does.
func TestLargeBodiesAreReadBackWhole(t *testing.T) {
	e := start(t, "wiremock")
	upload := largeUpload()
	e.mustSend(get("/wm/responses/large"))
	e.mustSend(post("/wm/responses/large-upload", upload, jsonCT, appJS))

	journal := journalOf(e) // most recent first
	if len(journal) != 2 {
		t.Fatalf("journal has %d entries, want 2", len(journal))
	}
	if got := len(journal[0].Body); got != len(upload) {
		t.Errorf("the journal kept %d bytes of the %d-byte request body", got, len(upload))
	}
	if got := len(journal[1].RespBody); got < 100_000 {
		t.Errorf("the journal kept %d bytes of a response of about 140000 bytes", got)
	}
	var items []any
	if err := json.Unmarshal([]byte(journal[1].RespBody), &items); err != nil || len(items) != 1400 {
		t.Errorf("the journalled response is not the 1400-item array (%d items, %v)", len(items), err)
	}
}

// withoutHost replaces a server's own address in header values, so a Location header that echoes
// the address the client used compares equal.
func withoutHost(values []string, base string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.ReplaceAll(v, base, "BASE")
	}
	return out
}

// TestKnownDifferencesAreDocumented needs no WireMock: every difference the comparison tolerates
// must be written up in docs/COMPATIBILITY.md, so none is hidden.
func TestKnownDifferencesAreDocumented(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(contract.RepoRoot(), "docs", "COMPATIBILITY.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for short, reason := range knownDifferences {
		mapping := ""
		for _, m := range loadMappings(t, "wiremock") {
			if strings.HasPrefix(m.ID, short[:8]) && strings.HasSuffix(m.ID, short[8:]) {
				mapping = fmt.Sprint(m.Response["fault"], m.Response["bodyFileName"], m.Request["urlPath"])
			}
		}
		if mapping == "" {
			t.Errorf("knownDifferences lists %s, which is not in the corpus", short)
		}
		if !strings.Contains(doc, "MALFORMED_RESPONSE_CHUNK") && strings.Contains(reason, "malformed") {
			t.Errorf("%s: %q is not described in docs/COMPATIBILITY.md", short, reason)
		}
	}
	for _, phrase := range []string{"missing `bodyFileName`", "MALFORMED_RESPONSE_CHUNK"} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("docs/COMPATIBILITY.md no longer mentions %q, which a known difference depends on", phrase)
		}
	}
}

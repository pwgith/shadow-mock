package conformance_test

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/test/contract"
)

// epoch is the engine's time, so relative dates in the corpus ("now -7 days") are repeatable.
var epoch = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

// env is an engine loaded with a copy of one corpus tree.
type env struct {
	t      *testing.T
	root   string
	base   string
	client *http.Client
	clk    *clock.Fake
}

// start copies corpus/<tree> to a temporary directory (the engine may write to it) and starts
// an engine over it. Delays complete instantly because the clock is fake.
func start(t *testing.T, tree string) *env {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join(contract.RepoRoot(), "corpus", tree))); err != nil {
		t.Fatalf("copy corpus/%s: %v", tree, err)
	}
	cfg := config.Default()
	cfg.Root = root
	cfg.Storage.Global.Watch = false
	clk := clock.NewFake(epoch)
	a, err := app.New(cfg, app.Options{Logger: slog.New(slog.DiscardHandler), Clock: clk, Version: "conformance"})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return &env{
		t: t, root: root, base: srv.URL, clk: clk,
		client: &http.Client{
			Timeout: 30 * time.Second,
			// No keep-alive, so a deliberate fault is not silently retried on a new connection; no
			// decompression, so the tests see Content-Encoding as sent.
			Transport:     &http.Transport{DisableCompression: true, DisableKeepAlives: true},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// call is one request to send.
type call struct {
	method string
	target string
	header map[string][]string
	body   string
}

// reply is what came back.
type reply struct {
	status int
	header http.Header
	body   []byte
}

func (e *env) send(c call) (*reply, error) {
	e.t.Helper()
	req, err := http.NewRequestWithContext(e.t.Context(), c.method, e.base+c.target, strings.NewReader(c.body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for k, vs := range c.header {
		if strings.EqualFold(k, "Host") {
			req.Host = vs[0]
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", c.method, c.target, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", c.method, c.target, err)
	}
	return &reply{status: resp.StatusCode, header: resp.Header, body: b}, nil
}

// mustSend sends and fails the test on a transport error.
func (e *env) mustSend(c call) *reply {
	e.t.Helper()
	r, err := e.send(c)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// admin calls the admin API and returns the decoded JSON body.
func (e *env) admin(method, path, body string) (*reply, map[string]any) {
	e.t.Helper()
	c := call{method: method, target: "/__admin" + path, body: body}
	if body != "" {
		c.header = map[string][]string{"Content-Type": {"application/json"}}
	}
	r := e.mustSend(c)
	var v map[string]any
	_ = json.Unmarshal(r.body, &v)
	return r, v
}

// latest returns the id of the mapping that served the most recent request ("" if none did).
// The journal is written before the response is sent (FR-5.5), so no waiting is needed.
func (e *env) latest() (stubID string, matched bool) {
	e.t.Helper()
	_, v := e.admin("GET", "/requests?limit=1", "")
	reqs, _ := v["requests"].([]any)
	if len(reqs) == 0 {
		e.t.Fatal("the journal is empty")
	}
	entry, _ := reqs[0].(map[string]any)
	matched, _ = entry["wasMatched"].(bool)
	if sm, ok := entry["stubMapping"].(map[string]any); ok {
		stubID, _ = sm["id"].(string)
	}
	return stubID, matched
}

// mapping is a corpus mapping file entry.
type mapping struct {
	ID            string
	Request       map[string]any
	Response      map[string]any
	CallResponses []map[string]any
	File          string
}

// loadMappings reads every mapping under corpus/<tree>/mappings.
func loadMappings(t *testing.T, tree string) []mapping {
	t.Helper()
	dir := filepath.Join(contract.RepoRoot(), "corpus", tree, "mappings")
	var out []mapping
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var doc struct {
			Mappings []map[string]any `json:"mappings"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		for _, m := range doc.Mappings {
			x := mapping{File: filepath.Base(p)}
			x.ID, _ = m["id"].(string)
			x.Request, _ = m["request"].(map[string]any)
			x.Response, _ = m["response"].(map[string]any)
			if cr, ok := m["callResponses"].([]any); ok {
				for _, r := range cr {
					rm, _ := r.(map[string]any)
					x.CallResponses = append(x.CallResponses, rm)
				}
			}
			out = append(out, x)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("load corpus/%s mappings: %v", tree, err)
	}
	return out
}

// find returns the mapping whose id starts with the first 8 characters of short and ends with
// its last 2, which is how the cases name them (for example "c005000101").
func find(ms []mapping, short string) (mapping, bool) {
	for _, m := range ms {
		if strings.HasPrefix(m.ID, short[:8]) && strings.HasSuffix(m.ID, short[8:]) {
			return m, true
		}
	}
	return mapping{}, false
}

// templated reports whether a response is rendered by the template engine, which is P3. For
// those only the status is compared.
func templated(resp map[string]any) bool {
	ts, _ := resp["transformers"].([]any)
	return slices.Contains(ts, any("response-template"))
}

// expectResponse checks a reply against the response definition in a mapping file. A non-zero
// wantStatus replaces the status the file gives and skips the rest of the comparison.
func (e *env) expectResponse(name string, def map[string]any, r *reply, wantStatus int) {
	e.t.Helper()
	want := 200
	if s, ok := def["status"].(float64); ok {
		want = int(s)
	}
	if wantStatus != 0 {
		want = wantStatus
	}
	if r.status != want {
		e.t.Errorf("%s: status = %d, want %d; body: %.300s", name, r.status, want, r.body)
		return
	}
	if templated(def) || wantStatus != 0 {
		return
	}
	if hs, ok := def["headers"].(map[string]any); ok {
		for k, v := range hs {
			var wantVals []string
			switch t := v.(type) {
			case string:
				wantVals = []string{t}
			case []any:
				for _, x := range t {
					wantVals = append(wantVals, fmt.Sprint(x))
				}
			}
			if got := r.header.Values(k); !slices.Equal(got, wantVals) {
				e.t.Errorf("%s: header %s = %q, want %q", name, k, got, wantVals)
			}
		}
	}
	got := r.body
	if r.header.Get("Content-Encoding") == "gzip" && !hasHeader(def, "Content-Encoding") {
		got = gunzip(e.t, got)
	}
	e.expectBody(name, def, got)
}

func hasHeader(def map[string]any, name string) bool {
	hs, _ := def["headers"].(map[string]any)
	for k := range hs {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

func (e *env) expectBody(name string, def map[string]any, got []byte) {
	e.t.Helper()
	switch {
	case def["body"] != nil:
		if want := fmt.Sprint(def["body"]); string(got) != want {
			e.t.Errorf("%s: body = %q, want %q", name, got, want)
		}
	case def["jsonBody"] != nil:
		var g any
		if err := json.Unmarshal(got, &g); err != nil || !reflect.DeepEqual(g, def["jsonBody"]) {
			e.t.Errorf("%s: body = %.300s (%v), want JSON %v", name, got, err, def["jsonBody"])
		}
	case def["base64Body"] != nil:
		want, _ := base64.StdEncoding.DecodeString(fmt.Sprint(def["base64Body"]))
		if !bytes.Equal(got, want) {
			e.t.Errorf("%s: body = %x, want %x", name, got, want)
		}
	case def["bodyFileName"] != nil:
		want, err := os.ReadFile(filepath.Join(e.root, "__files", fmt.Sprint(def["bodyFileName"])))
		if err != nil {
			e.t.Fatalf("%s: read body file: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			e.t.Errorf("%s: body (%d bytes) differs from %v (%d bytes)", name, len(got), def["bodyFileName"], len(want))
		}
	default:
		if len(got) != 0 {
			e.t.Errorf("%s: body = %.200q, want empty", name, got)
		}
	}
}

// expectRendered checks the body of a templated response against the text the case expects. Both
// are compared as JSON when they parse as JSON, so layout does not matter.
func (e *env) expectRendered(name, want string, got []byte) {
	e.t.Helper()
	var w, g any
	if json.Unmarshal([]byte(want), &w) == nil {
		if err := json.Unmarshal(got, &g); err != nil {
			e.t.Errorf("%s: the rendered body is not JSON: %v\n%s", name, err, got)
			return
		}
		if !reflect.DeepEqual(w, g) {
			wb, _ := json.MarshalIndent(w, "", "  ")
			gb, _ := json.MarshalIndent(g, "", "  ")
			e.t.Errorf("%s: the rendered body differs\nwant:\n%s\ngot:\n%s", name, wb, gb)
		}
		return
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(want) {
		e.t.Errorf("%s: the rendered body is %q, want %q", name, got, want)
	}
}

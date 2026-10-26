package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
)

// testEngine is a running engine for end-to-end tests.
type testEngine struct {
	t    *testing.T
	base string
	root string
	clk  *clock.Fake
}

func startEngine(t *testing.T, edit func(*config.Config)) *testEngine {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "mappings", "global.json"), `{"mappings":[
		{"id":"aaaaaaaa-0000-4000-8000-000000000001","request":{"method":"POST","urlPath":"/payments"},"response":{"status":200,"jsonBody":{"result":"approved"}}},
		{"request":{"method":"GET","urlPath":"/file"},"response":{"status":200,"bodyFileName":"hello.txt"}},
		{"request":{"method":"GET","urlPath":"/big"},"response":{"status":200,"body":"`+strings.Repeat("x", 2048)+`"}}]}`)
	write(t, filepath.Join(root, "mappings", "scenario.json"), `{"scenarioName":"s","request":{"urlPath":"/scenario"},"response":{}}`)
	write(t, filepath.Join(root, "__files", "hello.txt"), "global file")
	write(t, filepath.Join(root, "mapping-sets", "declined", "pay.json"), `{"request":{"method":"POST","urlPath":"/payments"},"response":{"status":402}}`)
	cfg := config.Default()
	cfg.Root = root
	cfg.Storage.Global.Watch = false
	if edit != nil {
		edit(&cfg)
	}
	clk := clock.NewFake(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	a, err := build(cfg, slog.New(slog.DiscardHandler), clk, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.close(); err != nil {
			t.Error(err)
		}
	})
	if err := a.start(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.mockRoute)
	t.Cleanup(srv.Close)
	return &testEngine{t: t, base: srv.URL, root: root, clk: clk}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

type reply struct {
	status int
	header http.Header
	body   string
}

func (e *testEngine) do(method, path, body string, header ...string) reply {
	e.t.Helper()
	req, err := http.NewRequestWithContext(e.t.Context(), method, e.base+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

func (e *testEngine) expect(r reply, status int, contains string) {
	e.t.Helper()
	if r.status != status || !strings.Contains(r.body, contains) {
		e.t.Errorf("got %d %q, want %d containing %q", r.status, r.body, status, contains)
	}
}

func field(t *testing.T, body, name string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("parse %s: %v", body, err)
	}
	s, ok := m[name].(string)
	if !ok {
		t.Fatalf("%s has no string %s", body, name)
	}
	return s
}

func TestGlobalSession(t *testing.T) {
	t.Parallel()
	e := startEngine(t, nil)
	e.expect(e.do("POST", "/payments", ""), 200, "approved")
	e.expect(e.do("GET", "/file", ""), 200, "global file")
	e.expect(e.do("GET", "/scenario", ""), 404, "Request was not matched")
	e.expect(e.do("GET", "/__admin/mappings", ""), 200, `"total":3`)
	e.expect(e.do("POST", "/__admin/mappings", `{"request":{"urlPath":"/new"},"response":{"body":"new"}}`), 201, `"id"`)
	e.expect(e.do("GET", "/new", ""), 200, "new")
	e.expect(e.do("POST", "/__admin/mappings/reset", ""), 200, "")
	e.expect(e.do("GET", "/new", ""), 404, "")
	e.expect(e.do("POST", "/__admin/requests/count", `{"method":"ANY","urlPathPattern":"/.*"}`), 200, `"count":5`)
	e.expect(e.do("GET", "/__admin/requests/unmatched", ""), 200, "/scenario")

	big := e.do("GET", "/big", "", "Accept-Encoding", "gzip")
	if big.header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("large response not gzipped: %v", big.header)
	}
	zr, err := gzip.NewReader(bytes.NewReader([]byte(big.body)))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil || len(plain) != 2048 {
		t.Errorf("gunzipped body = %d bytes, %v", len(plain), err)
	}
	e.expect(e.do("GET", "/local/file", ""), 200, "global file")
	e.expect(e.do("GET", "/__admin/scenarios", ""), 404, "not_supported")
	e.expect(e.do("GET", "/__admin/nope", ""), 404, "not_found")
	e.expect(e.do("POST", "/__admin/mappings/save", ""), 200, "")
}

func TestKeyBasedShadowMock(t *testing.T) {
	t.Parallel()
	e := startEngine(t, func(c *config.Config) { c.Respond.MatchedHeader = true })
	created := e.do("POST", "/__admin/sessions/key-based", `{"name":"declined","tokens":["CUST-99812"],
		"mappings":[{"request":{"method":"POST","urlPath":"/payments"},"response":{"status":402},
		"callResponses":[{"calls":"1","response":{"status":503}}]}]}`)
	e.expect(created, 201, `"key":"smk_`)
	key, id := field(t, created.body, "key"), field(t, created.body, "id")

	e.expect(e.do("POST", "/payments", ""), 200, "approved")
	first := e.do("POST", "/payments", "", "X-Shadow-Mock-Key", key)
	if first.status != 503 || !strings.Contains(first.header.Get("X-Shadow-Mock-Matched"), "layer=key-based") {
		t.Errorf("first session call = %d %v", first.status, first.header)
	}
	e.expect(e.do("POST", "/payments", `{"customer":"CUST-99812"}`), 402, "")
	e.expect(e.do("POST", "/__admin/sessions/"+id+"/requests/count", `{"urlPath":"/payments"}`), 200, `"count":2`)
	e.expect(e.do("GET", "/__admin/sessions/"+id+"/counters", ""), 200, `"count":2`)
	e.expect(e.do("GET", "/__admin/sessions/"+id+"/requests", ""), 200, `"stage":"ScanBody"`)
	e.expect(e.do("GET", "/__admin/sessions/"+id, ""), 200, `"keyPrefix":"`+key[:8])

	// Override by id and mask (FR-7.7, FR-7.8).
	e.expect(e.do("PATCH", "/__admin/sessions/"+id, `{"addMasks":[{"request":{"urlPath":"/file"}}]}`), 200, `"masks"`)
	e.expect(e.do("GET", "/file", "", "X-Shadow-Mock-Key", key), 404, "")
	e.expect(e.do("PUT", "/__admin/sessions/"+id+"/files/hello.txt", "session file", "Content-Type", "application/octet-stream"), 200, "")

	// Scoped admin through the header, and scenario rejection.
	e.expect(e.do("POST", "/__admin/mappings", `{"scenarioName":"x","request":{},"response":{}}`, "X-Shadow-Mock-Key", key), 422, "scenarios_not_supported")
	e.expect(e.do("POST", "/__admin/mappings", `{"request":{"urlPattern":"("},"response":{}}`, "X-Shadow-Mock-Key", key), 422, `"errors"`)

	e.expect(e.do("DELETE", "/__admin/sessions/"+id, ""), 200, "")
	e.expect(e.do("POST", "/payments", "", "X-Shadow-Mock-Key", key), 200, "approved")
	e.expect(e.do("GET", "/__admin/sessions/"+id, ""), 404, "not_found")
}

func TestURLBasedShadowMock(t *testing.T) {
	t.Parallel()
	e := startEngine(t, nil)
	created := e.do("POST", "/__admin/sessions/url-based", `{"seedFrom":{"mappingSet":"declined"}}`)
	e.expect(created, 201, `/d/`)
	id := field(t, created.body, "id")
	e.expect(e.do("POST", "/d/"+id+"/payments", ""), 402, "")
	e.expect(e.do("POST", "/local/d/"+id+"/payments", ""), 402, "")
	e.expect(e.do("GET", "/d/"+id+"/file", ""), 200, "global file")
	e.expect(e.do("GET", "/d/aaaaaaaaaaaaaaaa/file", ""), 404, "unknown_url_based_mock")

	nested := e.do("POST", "/__admin/sessions/key-based", `{"parentId":"`+id+`","mappings":[{"request":{"urlPath":"/payments"},"response":{"status":418}}]}`)
	e.expect(nested, 201, `"parentId":"`+id)
	key := field(t, nested.body, "key")
	e.expect(e.do("POST", "/d/"+id+"/payments", "", "X-Shadow-Mock-Key", key), 418, "")
	e.expect(e.do("POST", "/payments", "", "X-Shadow-Mock-Key", key), 200, "approved")
	e.expect(e.do("POST", "/__admin/sessions/url-based", `{"seedFrom":{"mappingSet":"missing"}}`), 422, "validation_failed")

	// Expiry: once the TTL passes, the mock is gone (FR-8.6).
	e.clk.Advance(25 * time.Hour)
	e.expect(e.do("GET", "/d/"+id+"/file", ""), 404, "unknown_url_based_mock")
}

func TestSeparateAdminListener(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Storage.Global.Driver = "memory"
	cfg.Listen.Admin = ":0"
	cfg.Tenancy.Local.IPAllowlist = []string{"10.0.0.0/8"}
	a, err := build(cfg, slog.New(slog.DiscardHandler), clock.NewFake(time.Unix(0, 0)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(a.handlers()) != 2 {
		t.Fatalf("handlers = %d, want 2", len(a.handlers()))
	}
	adminRec := httptest.NewRecorder()
	a.adminOnly.ServeHTTP(adminRec, httptest.NewRequestWithContext(t.Context(), "GET", "/__admin/version", http.NoBody))
	if adminRec.Code != 200 {
		t.Errorf("admin listener /__admin/version = %d", adminRec.Code)
	}
	mockRec := httptest.NewRecorder()
	a.mockRoute.ServeHTTP(mockRec, httptest.NewRequestWithContext(t.Context(), "GET", "/__admin/version", http.NoBody))
	if mockRec.Code != 404 {
		t.Errorf("mock listener served admin: %d", mockRec.Code)
	}
	blocked := httptest.NewRecorder()
	a.mockRoute.ServeHTTP(blocked, httptest.NewRequestWithContext(t.Context(), "GET", "/anything", http.NoBody))
	if blocked.Code != 403 || !strings.Contains(blocked.Body.String(), "ip_not_allowed") {
		t.Errorf("allowlist: %d %s", blocked.Code, blocked.Body)
	}
	if _, err := build(config.Config{Tenancy: config.Tenancy{Local: config.Local{IPAllowlist: []string{"nope"}}}, Storage: config.Storage{Global: config.GlobalStorage{Driver: "memory"}}}, slog.New(slog.DiscardHandler), clock.Real{}, nil); err == nil {
		t.Error("an invalid allowlist should fail to build")
	}
}

func TestRunVersionFlag(t *testing.T) {
	t.Parallel()
	if err := run([]string{"-version"}); err != nil {
		t.Errorf("run(-version) = %v", err)
	}
	if err := run([]string{"-nope"}); err == nil {
		t.Error("an unknown flag should fail")
	}
	if err := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}); err == nil {
		t.Error("a missing config file should fail")
	}
}

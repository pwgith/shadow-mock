package contract_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/test/contract"
)

// Environment for the live suite.
const (
	envBaseURL       = "SHADOWMOCK_CONTRACT_BASE_URL"       // e.g. http://localhost:8080 or https://shadowmock.kaizentis.xyz/acme
	envToken         = "SHADOWMOCK_CONTRACT_TOKEN"          // Clerk JWT for SaaS; unset in standalone
	envGlobalMapping = "SHADOWMOCK_CONTRACT_GLOBAL_MAPPING" // optional id of an existing global mapping (SaaS)
)

type liveRun struct {
	t         *testing.T
	base      *url.URL
	client    *http.Client
	transport *contract.ValidatingTransport
	token     string
	fixtures  map[string]string // placeholder → value
	created   map[contract.Fixture]bool
	cleanup   []func()
}

// TestLiveContract calls every operation on a running engine and validates each request and
// response against api/openapi.yaml. It is skipped unless SHADOWMOCK_CONTRACT_BASE_URL is set.
func TestLiveContract(t *testing.T) {
	raw := os.Getenv(envBaseURL)
	if raw == "" {
		t.Skipf("set %s to run the live contract suite against an engine", envBaseURL)
	}
	base, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil {
		t.Fatalf("parse %s: %v", envBaseURL, err)
	}
	s := loadSpec(t)
	tr := &contract.ValidatingTransport{Spec: s, Base: base}
	run := &liveRun{
		t:         t,
		base:      base,
		client:    &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: noRedirects},
		transport: tr,
		token:     os.Getenv(envToken),
		// {runId} keeps match tokens unique, so reruns against a shared engine never collide.
		fixtures: map[string]string{"{fileId}": "contract-fixture.json", "{runId}": strconv.FormatInt(time.Now().UnixNano(), 36)},
		created:  map[contract.Fixture]bool{},
	}
	t.Cleanup(func() {
		for i := len(run.cleanup) - 1; i >= 0; i-- {
			run.cleanup[i]()
		}
	})

	for _, c := range contract.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.SkipLive() {
				t.Skip("destructive in a shared environment; covered offline only")
			}
			for _, f := range c.Needs {
				run.ensure(t, f)
			}
			run.check(t, c)
		})
	}
}

func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (r *liveRun) fill(s string) string {
	for k, v := range r.fixtures {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

func (r *liveRun) do(t *testing.T, method, path, contentType, body string, header map[string]string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(r.fill(body))
	}
	// WithoutCancel: cleanups run after t.Context() is cancelled, and still need to send requests.
	req, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()), method, r.base.String()+r.fill(path), rdr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != "" {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range header {
		req.Header.Set(k, r.fill(v))
	}
	if r.token != "" && strings.Contains(path, "/__admin") {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, b
}

func (r *liveRun) check(t *testing.T, c contract.Case) {
	t.Helper()
	status, body := r.do(t, c.Method, c.Path, c.ContentType, c.Body, c.Header)
	if status == http.StatusCreated && strings.HasPrefix(c.Path, "/__admin/sessions/") {
		// Sessions created by a case are deleted at the end of the run, like fixtures.
		id := idFrom(t, status, body)
		r.cleanup = append(r.cleanup, func() { r.do(r.t, http.MethodDelete, "/__admin/sessions/"+id, "", "", nil) })
	}
	if !slices.Contains(c.WantStatus, status) {
		t.Errorf("status = %d, want one of %v; body: %s", status, c.WantStatus, truncate(body))
	}
	if c.WantError != "" {
		var e struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &e); err != nil || e.Error != c.WantError {
			t.Errorf("error code = %q, want %q; body: %s", e.Error, c.WantError, truncate(body))
		}
	}
	for _, v := range r.transport.TakeViolations() {
		if c.InvalidRequest && strings.Contains(v, "): request body") {
			continue
		}
		t.Errorf("contract violation: %s", v)
	}
}

// ensure creates a fixture once per run.
func (r *liveRun) ensure(t *testing.T, f contract.Fixture) {
	t.Helper()
	if r.created[f] {
		return
	}
	switch f {
	case contract.FixtureKeyBasedSession:
		id := r.createSession(t, "key-based", `{"name":"contract-fixture","ttlSeconds":900,"tokens":["CONTRACT-FIXTURE-{runId}"]}`)
		r.fixtures["{sessionId}"] = id
	case contract.FixtureURLBasedSession:
		id := r.createSession(t, "url-based", `{"name":"contract-fixture","ttlSeconds":900}`)
		r.fixtures["{urlMockId}"] = id
	case contract.FixtureGlobalMapping:
		if id := os.Getenv(envGlobalMapping); id != "" {
			r.fixtures["{stubMappingId}"] = id
			break
		}
		status, body := r.do(t, http.MethodPost, "/__admin/mappings", "",
			`{"request":{"method":"GET","urlPath":"/contract/fixture"},"response":{"status":200},"metadata":{"corpus":"contract"}}`, nil)
		if status == http.StatusForbidden {
			t.Skipf("global mappings are written by the management app in SaaS; set %s to an existing mapping id", envGlobalMapping)
		}
		id := idFrom(t, status, body)
		r.fixtures["{stubMappingId}"] = id
		r.cleanup = append(r.cleanup, func() { r.do(r.t, http.MethodDelete, "/__admin/mappings/"+id, "", "", nil) })
	case contract.FixtureSessionMapping:
		r.ensure(t, contract.FixtureKeyBasedSession)
		status, body := r.do(t, http.MethodPost, "/__admin/sessions/{sessionId}/mappings", "",
			`{"request":{"method":"GET","urlPath":"/contract/session-fixture"},"response":{"status":200},"callResponses":[{"calls":"1","response":{"status":503}}]}`, nil)
		r.fixtures["{sessionStubMappingId}"] = idFrom(t, status, body)
	case contract.FixtureSessionFile:
		r.ensure(t, contract.FixtureKeyBasedSession)
		status, body := r.do(t, http.MethodPut, "/__admin/sessions/{sessionId}/files/{fileId}", "application/octet-stream", `{"fixture":true}`, nil)
		if status != http.StatusOK {
			t.Fatalf("create session file: status %d: %s", status, truncate(body))
		}
	}
	for _, v := range r.transport.TakeViolations() {
		t.Errorf("contract violation while creating %s: %s", f, v)
	}
	r.created[f] = true
}

func (r *liveRun) createSession(t *testing.T, kind, body string) string {
	t.Helper()
	status, resp := r.do(t, http.MethodPost, "/__admin/sessions/"+kind, "", body, nil)
	id := idFrom(t, status, resp)
	r.cleanup = append(r.cleanup, func() { r.do(r.t, http.MethodDelete, "/__admin/sessions/"+id, "", "", nil) })
	return id
}

func idFrom(t *testing.T, status int, body []byte) string {
	t.Helper()
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create fixture: status %d: %s", status, truncate(body))
	}
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.ID == "" {
		t.Fatalf("create fixture: no id in %s", truncate(body))
	}
	return v.ID
}

func truncate(b []byte) string {
	const limit = 500
	if len(b) > limit {
		return string(b[:limit]) + "…"
	}
	return string(b)
}

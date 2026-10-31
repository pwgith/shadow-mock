package s3_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/blob"
	"github.com/pwgith/shadow-mock/internal/blob/s3"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
)

// These tests use Shadow Mock itself as a store that misbehaves: it answers with the errors, delays and dropped
// connections that a real object store gives when it is struggling (FR-11.14), which an in-memory fake cannot.
// They run against the engine built from this tree. Set SHADOWMOCK_EDGE_BIN to the path of a released build,
// such as the latest edge, and the same tests run against that binary in a process of its own.

// faultyStore returns the base URL of a Shadow Mock acting as an object store, and a function that adds a mapping.
func faultyStore(t *testing.T) (base string, stub func(mapping string)) {
	t.Helper()
	if bin := os.Getenv("SHADOWMOCK_EDGE_BIN"); bin != "" {
		base = startBinary(t, bin)
	} else {
		base = startInProcess(t)
	}
	return base, func(mapping string) {
		t.Helper()
		resp, err := http.Post(base+"/__admin/mappings", "application/json", strings.NewReader(mapping)) //nolint:noctx // a test server on loopback
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("stub %s: %d %s", mapping, resp.StatusCode, b)
		}
	}
}

func startInProcess(t *testing.T) string {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Storage.Global.Watch = false
	a, err := app.New(cfg, app.Options{Logger: slog.New(slog.DiscardHandler), Clock: clock.Real{}, Version: "test"})
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

func startBinary(t *testing.T, bin string) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, bin, "--root-dir", t.TempDir(), "--port", strconv.Itoa(port))
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for range 100 {
		if resp, err := http.Get(base + "/__health"); err == nil { //nolint:noctx // polling a test server
			_ = resp.Body.Close()
			return base
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the engine did not start")
	return ""
}

func client(t *testing.T, base string, timeout time.Duration) *s3.Client {
	t.Helper()
	c, err := s3.New(s3.Config{Endpoint: base, Bucket: "mocks", AccessKey: "AK", SecretKey: "SK", PathStyle: true, Timeout: timeout}, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func xmlError(code string) string {
	return fmt.Sprintf(`<?xml version="1.0"?><Error><Code>%s</Code><Message>x</Message></Error>`, code)
}

func mapping(method, path string, response, extra map[string]any) string {
	req := map[string]any{"method": method, "urlPath": path}
	for k, v := range extra {
		req[k] = v
	}
	b, err := json.Marshal(map[string]any{"request": req, "response": response})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestWhatAFailingStoreLooksLike(t *testing.T) {
	t.Parallel()
	base, stub := faultyStore(t)
	c := client(t, base, 300*time.Millisecond)
	ctx := t.Context()

	get := func(k string) error { _, err := c.Get(ctx, k, nil); return err }
	stat := func(k string) error { _, err := c.Stat(ctx, k); return err }
	put := func(k string) error { return c.Put(ctx, k, []byte("x"), blob.Meta{}) }
	del := func(k string) error { return c.Delete(ctx, k) }
	getPastEnd := func(k string) error {
		b, err := blob.ReadAll(ctx, c, k, &blob.Range{Off: 99})
		if err == nil && len(b) != 0 {
			return fmt.Errorf("got %q", b)
		}
		return err
	}
	status := func(code int, errCode string) map[string]any {
		return map[string]any{"status": code, "body": xmlError(errCode)}
	}

	type fault struct {
		name    string
		setup   map[string]any
		method  string
		do      func(key string) error
		wantErr error
		notErr  error
		message string
	}
	tests := []fault{
		{"a 503 on read is unavailable", status(503, "ServiceUnavailable"), "GET", get, blob.ErrUnavailable, nil, "ServiceUnavailable"},
		{"a throttled read is unavailable", status(429, "SlowDown"), "GET", get, blob.ErrUnavailable, nil, "SlowDown"},
		{"a 500 on write is unavailable", status(500, "InternalError"), "PUT", put, blob.ErrUnavailable, nil, "InternalError"},
		{"a slow store is unavailable", map[string]any{"status": 200, "body": "late", "fixedDelayMilliseconds": 1500}, "GET", get, blob.ErrUnavailable, nil, ""},
		{"a dropped connection is unavailable", map[string]any{"fault": "CONNECTION_RESET_BY_PEER"}, "GET", get, blob.ErrUnavailable, nil, ""},
		{"an empty reply is unavailable", map[string]any{"fault": "EMPTY_RESPONSE"}, "GET", get, blob.ErrUnavailable, nil, ""},
		{"access denied is not unavailable and not missing", status(403, "AccessDenied"), "GET", get, nil, blob.ErrUnavailable, "AccessDenied"},
		{"no such key is not found", status(404, "NoSuchKey"), "GET", get, blob.ErrNotFound, nil, ""},
		{"no such key on stat is not found", map[string]any{"status": 404}, "HEAD", stat, blob.ErrNotFound, nil, ""},
		{"a delete of a missing key is fine", status(404, "NoSuchKey"), "DELETE", del, nil, nil, ""},
		{"a range past the end is empty", status(416, "InvalidRange"), "GET", getPastEnd, nil, nil, ""},
	}
	for i, tt := range tests {
		key := fmt.Sprintf("k/case-%02d", i)
		stub(mapping(tt.method, "/mocks/"+key, tt.setup, nil))
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.do(key)
			switch {
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			case tt.wantErr == nil && tt.notErr == nil && err != nil:
				t.Errorf("error = %v, want none", err)
			case tt.notErr != nil && (err == nil || errors.Is(err, tt.notErr) || errors.Is(err, blob.ErrNotFound)):
				t.Errorf("error = %v, want a plain error that is not %v or not found", err, tt.notErr)
			case tt.message != "" && (err == nil || !strings.Contains(err.Error(), tt.message)):
				t.Errorf("error = %v, want it to say %q", err, tt.message)
			}
		})
	}
}

func TestRequestsAreSigned(t *testing.T) {
	t.Parallel()
	base, stub := faultyStore(t)
	c := client(t, base, 2*time.Second)
	// The mappings only answer a request that carries a SigV4 header and the hash of the body it sends, so an
	// unsigned or mis-hashed request would get the engine's 404 and fail the call.
	signed := map[string]any{
		"headers": map[string]any{
			"Authorization":        map[string]any{"matches": `AWS4-HMAC-SHA256 Credential=AK/\d{8}/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=[0-9a-f]{64}`},
			"X-Amz-Date":           map[string]any{"matches": `\d{8}T\d{6}Z`},
			"X-Amz-Content-Sha256": map[string]any{"equalTo": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
			"Content-Type":         map[string]any{"equalTo": "text/plain"},
		},
		"bodyPatterns": []any{map[string]any{"equalTo": "hello"}},
	}
	stub(mapping("PUT", "/mocks/files/acme/put", map[string]any{"status": 200}, signed))
	if err := c.Put(t.Context(), "files/acme/put", []byte("hello"), blob.Meta{ContentType: "text/plain"}); err != nil {
		t.Errorf("put: %v", err)
	}

	empty := map[string]any{"headers": map[string]any{
		"Authorization":        map[string]any{"matches": `AWS4-HMAC-SHA256 .*Signature=[0-9a-f]{64}`},
		"X-Amz-Content-Sha256": map[string]any{"equalTo": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		"Range":                map[string]any{"equalTo": "bytes=2-5"},
	}}
	stub(mapping("GET", "/mocks/files/acme/get", map[string]any{"status": 206, "body": "2345"}, empty))
	got, err := blob.ReadAll(t.Context(), c, "files/acme/get", &blob.Range{Off: 2, Len: 4})
	if err != nil || !bytes.Equal(got, []byte("2345")) {
		t.Errorf("get = %q, %v", got, err)
	}
}

func TestListFollowsContinuationTokens(t *testing.T) {
	t.Parallel()
	base, stub := faultyStore(t)
	c := client(t, base, 2*time.Second)
	page := func(truncated bool, token string, keys ...string) string {
		var b strings.Builder
		fmt.Fprintf(&b, `<?xml version="1.0"?><ListBucketResult><IsTruncated>%v</IsTruncated>`, truncated)
		if token != "" {
			fmt.Fprintf(&b, "<NextContinuationToken>%s</NextContinuationToken>", token)
		}
		for _, k := range keys {
			fmt.Fprintf(&b, "<Contents><Key>%s</Key></Contents>", k)
		}
		b.WriteString("</ListBucketResult>")
		return b.String()
	}
	stub(mapping("GET", "/mocks", map[string]any{"status": 200, "body": page(true, "t1", "files/acme/a", "files/acme/b")},
		map[string]any{"queryParameters": map[string]any{"list-type": map[string]any{"equalTo": "2"}, "prefix": map[string]any{"equalTo": "files/acme/"}, "continuation-token": map[string]any{"absent": true}}}))
	stub(mapping("GET", "/mocks", map[string]any{"status": 200, "body": page(false, "", "files/acme/c")},
		map[string]any{"queryParameters": map[string]any{"continuation-token": map[string]any{"equalTo": "t1"}}}))
	got, err := c.List(t.Context(), "files/acme/")
	if err != nil || strings.Join(got, ",") != "files/acme/a,files/acme/b,files/acme/c" {
		t.Errorf("list = %v, %v", got, err)
	}
}

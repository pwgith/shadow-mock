package respond_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/memory"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

var (
	globalScope  = store.Global(tenant.Local)
	sessionScope = store.Scope{Tenant: tenant.Local, SessionID: "ses_1"}
)

func def(t *testing.T, js string) *model.ResponseDefinition {
	t.Helper()
	var d model.ResponseDefinition
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatalf("response %s: %v", js, err)
	}
	return &d
}

func newRenderer(t *testing.T, gzipOn bool) (*respond.Renderer, *memory.Files, *clock.Fake) {
	t.Helper()
	files := memory.NewFiles()
	clk := clock.NewFake(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	return respond.NewRenderer(files, clk, gzipOn), files, clk
}

func TestRenderBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		def        string
		wantStatus int
		wantBody   string
		wantHeader map[string][]string
	}{
		{"nothing defined is an empty 200", `{}`, 200, "", nil},
		{"a text body", `{"status":201,"body":"hello"}`, 201, "hello", nil},
		{"jsonBody gets a JSON content type", `{"jsonBody":{"a":1}}`, 200, `{"a":1}`, map[string][]string{"Content-Type": {"application/json"}}},
		{
			"an explicit content type wins over the default", `{"jsonBody":{"a":1},"headers":{"Content-Type":"application/vnd.api+json"}}`, 200, `{"a":1}`,
			map[string][]string{"Content-Type": {"application/vnd.api+json"}},
		},
		{"base64Body is decoded", `{"base64Body":"aGVsbG8="}`, 200, "hello", nil},
		{
			"a header can have several values", `{"headers":{"Set-Cookie":["a=1","b=2"],"X-One":"1"}}`, 200, "",
			map[string][]string{"Set-Cookie": {"a=1", "b=2"}, "X-One": {"1"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _, _ := newRenderer(t, true)
			got, err := r.Render(t.Context(), def(t, tt.def), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tt.wantStatus || string(got.Body) != tt.wantBody {
				t.Errorf("Render = %d %q, want %d %q", got.Status, got.Body, tt.wantStatus, tt.wantBody)
			}
			for k, want := range tt.wantHeader {
				if diff := cmp.Diff(want, got.Header.Values(k)); diff != "" {
					t.Errorf("header %s (-want +got):\n%s", k, diff)
				}
			}
		})
	}
}

func TestRenderErrors(t *testing.T) {
	t.Parallel()
	r, _, _ := newRenderer(t, true)
	if _, err := r.Render(t.Context(), def(t, `{"base64Body":"%%%"}`), nil, nil); err == nil {
		t.Error("an invalid base64Body should fail")
	}
	_, err := r.Render(t.Context(), def(t, `{"proxyBaseUrl":"http://upstream"}`), nil, nil)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusNotImplemented || !strings.Contains(ae.Message, "http://upstream") {
		t.Errorf("a proxy response = %v, want 501 until proxying is built", err)
	}
}

func TestRenderBodyFilesPreferTheSession(t *testing.T) {
	t.Parallel()
	r, files, _ := newRenderer(t, true)
	ctx := t.Context()
	for _, f := range []struct {
		scope store.Scope
		name  string
		data  string
	}{
		{globalScope, "claim.json", "global"},
		{globalScope, "only-global.txt", "G"},
		{sessionScope, "claim.json", "session"},
	} {
		if err := files.Put(ctx, f.scope, f.name, []byte(f.data)); err != nil {
			t.Fatal(err)
		}
	}
	scopes := []store.Scope{sessionScope, globalScope} // most specific first
	for _, tt := range []struct{ file, scopesWant, want string }{
		{"claim.json", "session first", "session"},
		{"only-global.txt", "falls back to global", "G"},
	} {
		got, err := r.Render(ctx, def(t, `{"bodyFileName":"`+tt.file+`"}`), scopes, nil)
		if err != nil || string(got.Body) != tt.want {
			t.Errorf("%s (%s): %q, %v; want %q", tt.file, tt.scopesWant, got.Body, err, tt.want)
		}
	}
	if got, _ := r.Render(ctx, def(t, `{"bodyFileName":"claim.json"}`), []store.Scope{globalScope}, nil); string(got.Body) != "global" {
		t.Errorf("another session's file leaked: %q", got.Body)
	}
	_, err := r.Render(ctx, def(t, `{"bodyFileName":"nope.json"}`), scopes, nil)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Status != 500 || !strings.Contains(ae.Message, "nope.json") {
		t.Errorf("a missing file = %v, want a 500 naming the file", err)
	}
}

func TestDelays(t *testing.T) {
	t.Parallel()
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	t.Run("the response and the global setting add up", func(t *testing.T) {
		t.Parallel()
		r, _, _ := newRenderer(t, true)
		r.SetSettings(respond.Settings{FixedDelay: 50})
		if got := r.Settings().FixedDelay; got != 50 {
			t.Fatalf("Settings().FixedDelay = %d", got)
		}
		resp, _ := r.Render(t.Context(), def(t, `{"fixedDelayMilliseconds":100}`), nil, nil)
		if resp.Delay != ms(150) {
			t.Errorf("Delay = %v, want 150ms", resp.Delay)
		}
	})
	t.Run("a fixed distribution", func(t *testing.T) {
		t.Parallel()
		r, _, _ := newRenderer(t, true)
		resp, _ := r.Render(t.Context(), def(t, `{"delayDistribution":{"type":"fixed","milliseconds":70}}`), nil, nil)
		if resp.Delay != ms(70) {
			t.Errorf("Delay = %v", resp.Delay)
		}
	})
	t.Run("a uniform distribution stays in range", func(t *testing.T) {
		t.Parallel()
		r, _, _ := newRenderer(t, true)
		seen := map[time.Duration]bool{}
		for range 200 {
			resp, _ := r.Render(t.Context(), def(t, `{"delayDistribution":{"type":"uniform","lower":10,"upper":20}}`), nil, nil)
			if resp.Delay < ms(10) || resp.Delay > ms(20) {
				t.Fatalf("Delay = %v, want 10 to 20ms", resp.Delay)
			}
			seen[resp.Delay] = true
		}
		if len(seen) < 3 {
			t.Errorf("a uniform delay took only %d distinct values", len(seen))
		}
		resp, _ := r.Render(t.Context(), def(t, `{"delayDistribution":{"type":"uniform","lower":30,"upper":30}}`), nil, nil)
		if resp.Delay != ms(30) {
			t.Errorf("an empty range gives %v, want its lower bound", resp.Delay)
		}
	})
	t.Run("a log-normal distribution respects maxValue", func(t *testing.T) {
		t.Parallel()
		r, _, _ := newRenderer(t, true)
		for range 200 {
			resp, _ := r.Render(t.Context(), def(t, `{"delayDistribution":{"type":"lognormal","median":500,"sigma":2,"maxValue":40}}`), nil, nil)
			if resp.Delay > ms(40) {
				t.Fatalf("Delay = %v, want at most 40ms", resp.Delay)
			}
		}
		resp, _ := r.Render(t.Context(), def(t, `{"delayDistribution":{"type":"lognormal","median":100,"sigma":0}}`), nil, nil)
		if resp.Delay != ms(100) {
			t.Errorf("a log-normal with no spread = %v, want its median", resp.Delay)
		}
	})
	t.Run("the global distribution applies when the response has none", func(t *testing.T) {
		t.Parallel()
		r, _, _ := newRenderer(t, true)
		r.SetSettings(respond.Settings{DelayDistribution: &model.DelayDistribution{Type: "fixed", Milliseconds: 25}})
		resp, _ := r.Render(t.Context(), def(t, `{}`), nil, nil)
		if resp.Delay != ms(25) {
			t.Errorf("Delay = %v", resp.Delay)
		}
		resp, _ = r.Render(t.Context(), def(t, `{"delayDistribution":{"type":"fixed","milliseconds":5}}`), nil, nil)
		if resp.Delay != ms(5) {
			t.Errorf("the response's own distribution should win, got %v", resp.Delay)
		}
	})
	t.Run("an unknown distribution adds nothing", func(t *testing.T) {
		t.Parallel()
		r, _, _ := newRenderer(t, true)
		resp, _ := r.Render(t.Context(), def(t, `{"delayDistribution":{"type":"poisson"}}`), nil, nil)
		if resp.Delay != 0 {
			t.Errorf("Delay = %v", resp.Delay)
		}
	})
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestWriteGzip(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("claim ", 400)
	tests := []struct {
		name       string
		gzipOn     bool
		acceptEnc  string
		def        string
		wantGzip   bool
		wantHeader string
	}{
		{"a large body for a client that accepts gzip", true, "gzip", `{"body":"BIG"}`, true, ""},
		{"among several encodings", true, "br, gzip;q=0.8", `{"body":"BIG"}`, true, ""},
		{"gzip refused with q=0", true, "gzip;q=0", `{"body":"BIG"}`, false, ""},
		{"gzip refused with q=0.0 and spaces", true, "gzip ; q=0.0", `{"body":"BIG"}`, false, ""},
		{"a low but non-zero quality still allows gzip", true, "gzip;q=0.001", `{"body":"BIG"}`, true, ""},
		{"a client that does not mention gzip", true, "identity", `{"body":"BIG"}`, false, ""},
		{"no Accept-Encoding at all", true, "", `{"body":"BIG"}`, false, ""},
		{"gzipDisabled on the mapping", true, "gzip", `{"body":"BIG","gzipDisabled":true}`, false, ""},
		{"compression turned off in the engine", false, "gzip", `{"body":"BIG"}`, false, ""},
		{"a small body", true, "gzip", `{"body":"tiny"}`, false, ""},
		{"a body that is already encoded", true, "gzip", `{"body":"BIG","headers":{"Content-Encoding":"br"}}`, false, "br"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _, _ := newRenderer(t, tt.gzipOn)
			resp, err := r.Render(t.Context(), def(t, strings.ReplaceAll(tt.def, "BIG", big)), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tt.acceptEnc != "" {
				req.Header.Set("Accept-Encoding", tt.acceptEnc)
			}
			rec := httptest.NewRecorder()
			if err := r.Write(t.Context(), rec, req, resp, nil); err != nil {
				t.Fatal(err)
			}
			enc := rec.Header().Get("Content-Encoding")
			if tt.wantGzip {
				if enc != "gzip" || !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
					t.Fatalf("Content-Encoding %q, Vary %q; want gzip", enc, rec.Header().Get("Vary"))
				}
				if gunzip(t, rec.Body.Bytes()) != big {
					t.Error("the gzipped body does not decode to the original")
				}
			} else if enc != tt.wantHeader {
				t.Errorf("Content-Encoding = %q, want %q", enc, tt.wantHeader)
			}
			if got := rec.Header().Get("Content-Length"); got != itoa(rec.Body.Len()) {
				t.Errorf("Content-Length = %s, body is %d bytes", got, rec.Body.Len())
			}
		})
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestWriteWaitsForTheDelayThenRecordsBeforeSending(t *testing.T) {
	t.Parallel()
	r, _, clk := newRenderer(t, true)
	start := clk.Now()
	resp, _ := r.Render(t.Context(), def(t, `{"status":202,"body":"slow","fixedDelayMilliseconds":2000}`), nil, nil)
	rec := httptest.NewRecorder()
	var sentBefore bool
	var timeAtRecord time.Time
	err := r.Write(t.Context(), rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody), resp, func() {
		sentBefore = rec.Body.Len() > 0 || rec.Flushed
		timeAtRecord = clk.Now()
	})
	if err != nil {
		t.Fatal(err)
	}
	if sentBefore {
		t.Error("beforeSend ran after the response was written")
	}
	if timeAtRecord.Sub(start) != 2*time.Second {
		t.Errorf("beforeSend ran %v after the start, want after the 2s delay", timeAtRecord.Sub(start))
	}
	if rec.Code != 202 || rec.Body.String() != "slow" {
		t.Errorf("response %d %q", rec.Code, rec.Body)
	}
}

func TestWriteGivesUpWhenTheClientLeavesDuringTheDelay(t *testing.T) {
	t.Parallel()
	r, _, _ := newRenderer(t, true)
	resp, _ := r.Render(t.Context(), def(t, `{"fixedDelayMilliseconds":1000}`), nil, nil)
	ctx, cancel := cancelledContext(t)
	defer cancel()
	called := false
	rec := httptest.NewRecorder()
	err := r.Write(ctx, rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody), resp, func() { called = true })
	if err == nil || called || rec.Body.Len() != 0 {
		t.Errorf("Write = %v, beforeSend called %v, body %q; want an error and nothing sent", err, called, rec.Body)
	}
}

func TestWriteDribble(t *testing.T) {
	t.Parallel()
	r, _, clk := newRenderer(t, true)
	start := clk.Now()
	resp, _ := r.Render(t.Context(), def(t, `{"body":"0123456789ABCDEFGHIJ","chunkedDribbleDelay":{"numberOfChunks":4,"totalDuration":400}}`), nil, nil)
	rec := httptest.NewRecorder()
	if err := r.Write(t.Context(), rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody), resp, nil); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "0123456789ABCDEFGHIJ" || !rec.Flushed {
		t.Errorf("body %q, flushed %v", rec.Body, rec.Flushed)
	}
	// Four chunks, with a pause of 100ms between each pair.
	if got := clk.Now().Sub(start); got != 300*time.Millisecond {
		t.Errorf("the dribble took %v of fake time, want 300ms", got)
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("a dribbled body is sent chunked, without a Content-Length")
	}
}

func TestWriteDribbleStopsWhenTheClientLeaves(t *testing.T) {
	t.Parallel()
	r, _, _ := newRenderer(t, true)
	resp, _ := r.Render(t.Context(), def(t, `{"body":"0123456789","chunkedDribbleDelay":{"numberOfChunks":5,"totalDuration":500}}`), nil, nil)
	ctx, cancel := cancelledContext(t)
	defer cancel()
	err := r.Write(ctx, httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody), resp, nil)
	if err == nil {
		t.Error("a cancelled dribble should report an error")
	}
}

func TestFaultsBreakTheConnection(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{model.FaultConnectionReset, model.FaultEmptyResponse, model.FaultMalformedChunk, model.FaultRandomDataThenClose} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			r, _, _ := newRenderer(t, true)
			resp, _ := r.Render(t.Context(), def(t, `{"fault":"`+fault+`"}`), nil, nil)
			// The handler runs on the server's goroutine and may still be running when the client
			// sees the broken connection, so its outcome is handed over on a channel.
			type outcome struct {
				err      error
				recorded bool
			}
			done := make(chan outcome, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				recorded := false
				err := r.Write(req.Context(), w, req, resp, func() { recorded = true })
				done <- outcome{err: err, recorded: recorded}
			}))
			defer srv.Close()
			client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 10 * time.Second}
			got, err := client.Get(srv.URL) //nolint:noctx // the connection is broken on purpose
			if err == nil {
				_, err = io.ReadAll(got.Body)
				got.Body.Close()
			}
			if err == nil {
				t.Errorf("%s: the client received a complete response", fault)
			}
			select {
			case got := <-done:
				if got.err != nil || !got.recorded {
					t.Errorf("%s: Write = %v, beforeSend called %v", fault, got.err, got.recorded)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: the handler did not finish", fault)
			}
		})
	}
}

func TestFaultNeedsAHijackableConnection(t *testing.T) {
	t.Parallel()
	r, _, _ := newRenderer(t, true)
	resp, _ := r.Render(t.Context(), def(t, `{"fault":"EMPTY_RESPONSE"}`), nil, nil)
	err := r.Write(t.Context(), httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody), resp, nil)
	if err == nil {
		t.Error("a fault on a writer that cannot be hijacked should report an error")
	}
}

// cancelledContext returns an already cancelled context.
func cancelledContext(t *testing.T) (ctx context.Context, cancel context.CancelFunc) {
	t.Helper()
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	return ctx, cancel
}

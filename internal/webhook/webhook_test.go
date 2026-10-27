package webhook_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/egress"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/respond/template"
	"github.com/pwgith/shadow-mock/internal/webhook"
)

// syncBuffer is a log destination that the dispatcher's goroutines can write to safely.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForLog(t *testing.T, logs *syncBuffer, want string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if strings.Contains(logs.String(), want) {
			return
		}
	}
	t.Errorf("log lacks %q:\n%s", want, logs.String())
}

type got struct {
	method, path, body string
	header             http.Header
}

// target records the calls it receives.
type target struct {
	*httptest.Server
	mu    sync.Mutex
	calls []got
	ch    chan got
}

func newTarget(t *testing.T, status int) *target {
	t.Helper()
	tg := &target{ch: make(chan got, 16)}
	tg.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c := got{r.Method, r.URL.RequestURI(), string(b), r.Header.Clone()}
		tg.mu.Lock()
		tg.calls = append(tg.calls, c)
		tg.mu.Unlock()
		tg.ch <- c
		w.WriteHeader(status)
	}))
	t.Cleanup(tg.Close)
	return tg
}

func (tg *target) next(t *testing.T) got {
	t.Helper()
	select {
	case c := <-tg.ch:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("the webhook target received nothing")
		return got{}
	}
}

func dispatcher(t *testing.T, logs *syncBuffer, opts egress.Options) *webhook.Dispatcher {
	t.Helper()
	clk := clock.Real{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := webhook.New(egress.New(opts), template.New(clk), clk, logger)
	t.Cleanup(d.Close)
	return d
}

func hooks(t *testing.T, js string) []model.Webhook {
	t.Helper()
	m, err := model.DecodeStubMapping([]byte(`{"request":{"urlPath":"/x"},"serveEventListeners":[{"name":"webhook","parameters":` + js + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return m.Webhooks
}

func fire(t *testing.T, d *webhook.Dispatcher, hs []model.Webhook, body string) {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://mock/payments", strings.NewReader(body))
	in := &respond.Input{Request: match.NewRequest(r, []byte(body), "/payments")}
	d.Fire(hs, in, map[string]any{"k": "v"}, &respond.Response{Status: 202, Header: http.Header{"X-A": {"1"}}, Body: []byte("ok")})
}

func TestFireSendsATemplatedCall(t *testing.T) {
	t.Parallel()
	tg := newTarget(t, http.StatusOK)
	var logs syncBuffer
	d := dispatcher(t, &logs, egress.Options{})
	fire(t, d, hooks(t, `{"method":"POST","url":"`+tg.URL+`/cb/{{jsonPath originalRequest.body '$.ref'}}",
		"headers":{"X-Status":"{{response.status}}","Content-Type":"application/json"},
		"body":"{\"ref\":\"{{jsonPath originalRequest.body '$.ref'}}\",\"p\":\"{{parameters.k}}\"}"}`), `{"ref":"P-77"}`)

	c := tg.next(t)
	if c.method != http.MethodPost || c.path != "/cb/P-77" {
		t.Errorf("call = %s %s, want POST /cb/P-77", c.method, c.path)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(c.body), &body); err != nil || body["ref"] != "P-77" || body["p"] != "v" {
		t.Errorf("body = %q (%v)", c.body, err)
	}
	if c.header.Get("X-Status") != "202" {
		t.Errorf("X-Status = %q, want 202 from the served response", c.header.Get("X-Status"))
	}
}

func TestFireWaitsForTheDelay(t *testing.T) {
	t.Parallel()
	tg := newTarget(t, http.StatusOK)
	var logs syncBuffer
	d := dispatcher(t, &logs, egress.Options{})
	start := time.Now()
	fire(t, d, hooks(t, `{"method":"GET","url":"`+tg.URL+`/later","delay":{"type":"fixed","milliseconds":300}}`), "")
	select {
	case <-tg.ch:
		t.Fatal("the webhook was sent before its delay")
	case <-time.After(150 * time.Millisecond):
	}
	tg.next(t)
	if time.Since(start) < 300*time.Millisecond {
		t.Errorf("sent after %v, want at least 300ms", time.Since(start))
	}
}

func TestFireBase64BodyAndSeveralHooks(t *testing.T) {
	t.Parallel()
	tg := newTarget(t, http.StatusOK)
	var logs syncBuffer
	d := dispatcher(t, &logs, egress.Options{})
	hs := append(hooks(t, `{"method":"PUT","url":"`+tg.URL+`/a","base64Body":"aGVsbG8="}`),
		hooks(t, `{"method":"DELETE","url":"`+tg.URL+`/b"}`)...)
	fire(t, d, hs, "")
	seen := map[string]string{}
	for range 2 {
		c := tg.next(t)
		seen[c.method+" "+c.path] = c.body
	}
	if seen["PUT /a"] != "hello" {
		t.Errorf("PUT /a body = %q, want hello (seen %v)", seen["PUT /a"], seen)
	}
	if _, ok := seen["DELETE /b"]; !ok {
		t.Errorf("DELETE /b was not sent (seen %v)", seen)
	}
}

func TestFailuresAreLoggedNotRaised(t *testing.T) {
	t.Parallel()
	var logs syncBuffer
	d := dispatcher(t, &logs, egress.Options{BlockPrivate: true})
	tg := newTarget(t, http.StatusInternalServerError)
	fire(t, d, hooks(t, `{"method":"GET","url":"`+tg.URL+`/blocked"}`), "")
	fire(t, d, hooks(t, `{"method":"GET","url":"http://{{nope"}`), "")
	fire(t, d, hooks(t, `{"method":"GET","url":"http://example.test/{{math 1 '/' 0}}"}`), "")
	for _, want := range []string{"egress_blocked", "webhook failed"} {
		waitForLog(t, &logs, want)
	}
	if n := tg.count(); n != 0 {
		t.Errorf("the blocked target was called %d times", n)
	}
}

func TestAServerErrorFromTheTargetIsNotAFailure(t *testing.T) {
	t.Parallel()
	tg := newTarget(t, http.StatusInternalServerError)
	var logs syncBuffer
	d := dispatcher(t, &logs, egress.Options{})
	fire(t, d, hooks(t, `{"method":"GET","url":"`+tg.URL+`/x"}`), "")
	tg.next(t)
	d.Close()
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("a 500 from the target was logged as a warning:\n%s", logs.String())
	}
}

func TestCloseCancelsPendingHooks(t *testing.T) {
	t.Parallel()
	tg := newTarget(t, http.StatusOK)
	var logs syncBuffer
	d := dispatcher(t, &logs, egress.Options{})
	fire(t, d, hooks(t, `{"method":"GET","url":"`+tg.URL+`/never","delay":{"type":"fixed","milliseconds":5000}}`), "")
	done := make(chan struct{})
	go func() { d.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel the delayed webhook")
	}
	if tg.count() != 0 {
		t.Error("a cancelled webhook was still sent")
	}
}

func TestFireWithNothingToDo(t *testing.T) {
	t.Parallel()
	var logs syncBuffer
	d := dispatcher(t, &logs, egress.Options{})
	d.Fire(nil, nil, nil, &respond.Response{}) // no hooks, no request: nothing happens
	fire(t, d, nil, "")
}

func (tg *target) count() int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return len(tg.calls)
}

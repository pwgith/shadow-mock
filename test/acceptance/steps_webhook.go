package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/cucumber/godog"
	"github.com/ohler55/ojg/jp"
)

// received is one request a webhook receiver got.
type received struct {
	method string
	path   string
	body   []byte
}

// receiver is a stand-in for the system a webhook calls. It records what it is sent.
type receiver struct {
	srv *httptest.Server

	mu     sync.Mutex
	status int
	got    []received
}

func newReceiver() *receiver {
	r := &receiver{status: http.StatusOK}
	r.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body) //nolint:errcheck // a short read only makes the assertion fail
		r.mu.Lock()
		r.got = append(r.got, received{method: req.Method, path: req.URL.RequestURI(), body: body})
		status := r.status
		r.mu.Unlock()
		rw.WriteHeader(status)
	}))
	return r
}

// find returns the first request matching "METHOD /path" that satisfies check.
func (r *receiver) find(method, path string, check func([]byte) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.got {
		if g.method == method && g.path == path && (check == nil || check(g.body)) {
			return true
		}
	}
	return false
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

// eventually polls until cond holds or the time is up.
func eventually(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// receiverNamed returns a started receiver.
func (w *world) receiverNamed(name string) (*receiver, error) {
	r, ok := w.receivers[name]
	if !ok {
		return nil, fmt.Errorf("the webhook receiver %q is not listening", name)
	}
	return r, nil
}

func (w *world) closeReceivers() {
	for _, r := range w.receivers {
		r.srv.Close()
	}
}

// initWebhook registers the steps for webhook receivers. A receiver's URL is available to the
// scenario as ${webhook.NAME}.
func (w *world) initWebhook(sc *godog.ScenarioContext) {
	sc.Step(`^the webhook receiver "([^"]*)" is listening$`, func(name string) error {
		if w.receivers == nil {
			w.receivers = map[string]*receiver{}
		}
		r := newReceiver()
		w.receivers[name] = r
		w.vars["webhook."+name] = r.srv.URL
		return nil
	})
	sc.Step(`^the webhook receiver "([^"]*)" responds (\d+) to everything$`, func(name string, status int) error {
		if w.receivers == nil {
			w.receivers = map[string]*receiver{}
		}
		r, ok := w.receivers[name]
		if !ok {
			r = newReceiver()
			w.receivers[name] = r
			w.vars["webhook."+name] = r.srv.URL
		}
		r.mu.Lock()
		r.status = status
		r.mu.Unlock()
		return nil
	})
	sc.Step(`^within (\d+) s the webhook receiver "([^"]*)" receives "([A-Z]+) ([^"]*)"$`, func(s int, name, method, path string) error {
		r, err := w.receiverNamed(name)
		if err != nil {
			return err
		}
		if !eventually(time.Duration(s)*time.Second, func() bool { return r.find(method, path, nil) }) {
			return fmt.Errorf("%s did not receive %s %s within %d s (it got %d requests)", name, method, path, s, r.count())
		}
		return nil
	})
	sc.Step(`^within (\d+) s the webhook receiver "([^"]*)" receives "([A-Z]+) ([^"]*)" with JSON at "([^"]*)" equal to "([^"]*)"$`,
		func(s int, name, method, path, expr, want string) error {
			r, err := w.receiverNamed(name)
			if err != nil {
				return err
			}
			x, err := jp.ParseString(expr)
			if err != nil {
				return fmt.Errorf("parse JSONPath %q: %w", expr, err)
			}
			has := func(body []byte) bool {
				var doc any
				if json.Unmarshal(body, &doc) != nil {
					return false
				}
				res := x.Get(doc)
				return len(res) == 1 && fmt.Sprint(res[0]) == w.sub(want)
			}
			if !eventually(time.Duration(s)*time.Second, func() bool { return r.find(method, path, has) }) {
				return fmt.Errorf("%s did not receive %s %s with %s = %q within %d s", name, method, path, expr, want, s)
			}
			return nil
		})
	sc.Step(`^the webhook receiver "([^"]*)" receives nothing within (\d+) ms$`, func(name string, ms int) error {
		r, err := w.receiverNamed(name)
		if err != nil {
			return err
		}
		if eventually(time.Duration(ms)*time.Millisecond, func() bool { return r.count() > 0 }) {
			return fmt.Errorf("%s received a request within %d ms", name, ms)
		}
		return nil
	})
}

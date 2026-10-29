package acceptance

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/cucumber/godog"
)

// canned is one answer an upstream stub server gives.
type canned struct {
	status      int
	body        string
	contentType string
}

// seen is a request an upstream stub server received.
type seen struct {
	method string
	target string
	header http.Header
}

// upstream is a stand-in for a real server behind a proxy or a recording. Each route holds
// replies it gives in turn; the last one repeats.
type upstream struct {
	srv *httptest.Server

	mu     sync.Mutex
	routes map[string][]canned
	calls  map[string]int
	got    []seen
}

func newUpstream() *upstream {
	u := &upstream{routes: map[string][]canned{}, calls: map[string]int{}}
	u.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) //nolint:errcheck // the body is not used
		key := r.Method + " " + r.URL.RequestURI()
		u.mu.Lock()
		h := r.Header.Clone()
		h.Set("Host", r.Host) // Go moves the Host header out of the header map
		u.got = append(u.got, seen{r.Method, r.URL.RequestURI(), h})
		replies := u.routes[key]
		n := u.calls[key]
		u.calls[key]++
		u.mu.Unlock()
		if len(replies) == 0 {
			http.NotFound(rw, r)
			return
		}
		rep := replies[min(n, len(replies)-1)]
		if rep.contentType != "" {
			rw.Header().Set("Content-Type", rep.contentType)
		}
		rw.WriteHeader(rep.status)
		_, _ = io.WriteString(rw, rep.body) //nolint:errcheck // the proxy may hang up
	}))
	return u
}

func (u *upstream) set(route string, replies ...canned) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.routes[route] = replies
	u.calls[route] = 0
}

// requests returns a copy of what the server has received.
func (u *upstream) requests() []seen {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]seen(nil), u.got...)
}

func (w *world) upstreamNamed(name string) (*upstream, error) {
	u, ok := w.upstreams[name]
	if !ok {
		return nil, fmt.Errorf("no upstream stub server %q", name)
	}
	return u, nil
}

// stubUpstream creates the server on first use and gives it a route. The server's URL is
// available to the scenario as ${upstream.NAME}.
func (w *world) stubUpstream(name, route string, replies ...canned) {
	if w.upstreams == nil {
		w.upstreams = map[string]*upstream{}
	}
	u, ok := w.upstreams[name]
	if !ok {
		u = newUpstream()
		w.upstreams[name] = u
		w.vars["upstream."+name] = u.srv.URL
	}
	u.set(route, replies...)
}

func (w *world) closeUpstreams() {
	for _, u := range w.upstreams {
		u.srv.Close()
	}
}

// initUpstream registers the steps for upstream stub servers.
func (w *world) initUpstream(sc *godog.ScenarioContext) {
	sc.Step(`^the upstream stub server "([^"]*)" returns (\d+) with body "([^"]*)" for "([^"]*)"$`, func(name string, status int, body, route string) error {
		w.stubUpstream(name, route, canned{status: status, body: body, contentType: "text/plain"})
		return nil
	})
	sc.Step(`^the upstream stub server "([^"]*)" returns (\d+) with JSON '([^']*)' for "([^"]*)"$`, func(name string, status int, body, route string) error {
		w.stubUpstream(name, route, canned{status: status, body: body, contentType: "application/json"})
		return nil
	})
	sc.Step(`^the upstream stub server "([^"]*)" returns (\d+(?: then \d+)+) for "([^"]*)"$`, func(name, statuses, route string) error {
		var replies []canned
		for _, f := range strings.Split(statuses, " then ") {
			n, err := strconv.Atoi(f)
			if err != nil {
				return fmt.Errorf("status %q: %w", f, err)
			}
			replies = append(replies, canned{status: n, body: `{"id":1}`, contentType: "application/json"})
		}
		w.stubUpstream(name, route, replies...)
		return nil
	})
	sc.Step(`^the upstream stub server "([^"]*)" returns "([^"]*)" then "([^"]*)" for "([^"]*)"$`, func(name, first, second, route string) error {
		w.stubUpstream(name, route,
			canned{status: http.StatusOK, body: first, contentType: "text/plain"},
			canned{status: http.StatusOK, body: second, contentType: "text/plain"})
		return nil
	})
	sc.Step(`^the upstream stub server "([^"]*)" is stopped$`, func(name string) error {
		u, err := w.upstreamNamed(name)
		if err != nil {
			return err
		}
		u.srv.Close()
		return nil
	})
	sc.Step(`^the upstream "([^"]*)" received "([^"]*)"$`, func(name, route string) error {
		u, err := w.upstreamNamed(name)
		if err != nil {
			return err
		}
		for _, s := range u.requests() {
			if s.method+" "+s.target == route {
				return nil
			}
		}
		return fmt.Errorf("upstream %s did not receive %s", name, route)
	})
	sc.Step(`^the upstream "([^"]*)" received header "([^"]*)" with value "([^"]*)"$`, func(name, header, want string) error {
		u, err := w.upstreamNamed(name)
		if err != nil {
			return err
		}
		for _, s := range u.requests() {
			if s.header.Get(header) == want {
				return nil
			}
		}
		return fmt.Errorf("upstream %s received no request with %s: %s", name, header, want)
	})
	sc.Step(`^the upstream "([^"]*)" did not receive header "([^"]*)"$`, func(name, header string) error {
		u, err := w.upstreamNamed(name)
		if err != nil {
			return err
		}
		for _, s := range u.requests() {
			if v := s.header.Get(header); v != "" {
				return fmt.Errorf("upstream %s received %s: %s", name, header, v)
			}
		}
		return nil
	})
	sc.Step(`^I record "([A-Z]+) ([^"]*)" from "([^"]*)"$`, func(method, path, target string) error {
		if _, err := w.mustAdmin("POST", "/recordings/start", `{"targetBaseUrl":"`+w.sub(target)+`"}`, http.StatusOK); err != nil {
			return err
		}
		if err := w.sendSimple(method, path, nil, ""); err != nil {
			return err
		}
		_, err := w.mustAdmin("POST", "/recordings/stop", "", http.StatusOK)
		return err
	})
}

// initRecordingAsserts registers the steps that check recordings and the mappings they make.
func (w *world) initRecordingAsserts(sc *godog.ScenarioContext) {
	sc.Step(`^admin "GET" "([^"]*)" returns status "([^"]*)"$`, func(path, want string) error {
		r, err := w.adminCall("GET", path, "", nil)
		if err != nil {
			return err
		}
		got, err := jsonAt(r.body, "$.status")
		if err != nil || len(got) != 1 || text(got[0]) != want {
			return fmt.Errorf("GET %s status = %v (%v), want %s", path, got, err, want)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is present$`, func(path string) error {
		if _, found, err := w.jsonOne(path); err != nil || !found {
			return fmt.Errorf("%s is absent (%v), want it present", path, err)
		}
		return nil
	})
	sc.Step(`^admin "GET" "([^"]*)" does not include a mapping for "([^"]*)"$`, func(path, url string) error {
		r, err := w.adminCall("GET", path, "", nil)
		if err != nil {
			return err
		}
		for _, field := range []string{"url", "urlPath"} {
			got, err := jsonAt(r.body, "$.mappings[*].request."+field)
			if err != nil {
				return err
			}
			for _, g := range got {
				if text(g) == url {
					return fmt.Errorf("GET %s lists a mapping for %s", path, url)
				}
			}
		}
		return nil
	})
	sc.Step(`^I send (\d+) "([^"]*)" requests to "([^"]*)" via "([^"]*)"$`, func(n int, m, p, name string) error {
		for range n {
			if err := w.sendSimple(m, w.urlBasedPath(name, p), nil, ""); err != nil {
				return err
			}
		}
		return nil
	})
}

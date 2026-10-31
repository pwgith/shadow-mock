package acceptance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

// usageBucket is one hour of an org's usage, as GET /usage answers it.
type usageBucket struct {
	PeriodStart           time.Time `json:"periodStart"`
	PeriodEnd             time.Time `json:"periodEnd"`
	Requests              int64     `json:"requests"`
	ConcurrentSessionsMax int       `json:"concurrentSessionsMax"`
	StoredFiles           struct {
		Count int   `json:"count"`
		Bytes int64 `json:"bytes"`
	} `json:"storedFiles"`
	RateLimited int64 `json:"rateLimited"`
	IPRejected  int64 `json:"ipRejected"`
	Revision    int   `json:"revision"`
}

// sendBurst sends n requests one after another, as fast as the engine answers, and keeps the replies.
func (w *world) sendBurst(n int, method, path string) error {
	w.batch, w.batchPaths = nil, nil
	for range n {
		rep, err := w.send(newRequest(method, path))
		if err != nil {
			return err
		}
		w.batch = append(w.batch, rep)
	}
	return nil
}

// usageNow reads the org's usage with the harness's own credential.
func (w *world) usageNow(org string) ([]usageBucket, error) {
	rep, err := w.managedAdminCall("GET", "/usage", "", org, w.setupCred(org), nil)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Buckets []usageBucket `json:"buckets"`
	}
	if rep.status != http.StatusOK {
		return nil, fmt.Errorf("GET /usage gave %d: %s", rep.status, truncate(rep.body))
	}
	if err = json.Unmarshal(rep.body, &doc); err != nil {
		return nil, fmt.Errorf("parse the usage: %w", err)
	}
	return doc.Buckets, nil
}

// usageTotal adds up one measure over the buckets.
func usageTotal(bs []usageBucket, measure func(usageBucket) int64) int64 {
	var n int64
	for _, b := range bs {
		n += measure(b)
	}
	return n
}

func (w *world) initManagedLimits(sc *godog.ScenarioContext) {
	sendBurst := func(n int, method, path string) error { return w.sendBurst(n, method, path) }
	sc.Step(`^the SUT sends (\d+) requests in one second to "([A-Z]+) ([^"]*)"$`, sendBurst)
	sc.Step(`^the SUT is sending (\d+) requests a second to "([A-Z]+) ([^"]*)"$`, sendBurst)
	sc.Step(`^some responses are 429$`, func() error {
		for _, r := range w.batch {
			if r.status == http.StatusTooManyRequests {
				return nil
			}
		}
		return fmt.Errorf("none of the %d responses was 429", len(w.batch))
	})
	each429 := func(check func(*reply) error) error {
		n := 0
		for _, r := range w.batch {
			if r.status != http.StatusTooManyRequests {
				continue
			}
			n++
			if err := check(r); err != nil {
				return err
			}
		}
		if n == 0 {
			return fmt.Errorf("there was no 429 to check")
		}
		return nil
	}
	sc.Step(`^each 429 has the error "([^"]*)"$`, func(code string) error {
		return each429(func(r *reply) error {
			var doc struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(r.body, &doc) != nil || doc.Error != code {
				return fmt.Errorf("a 429 has the body %s, want the error %q", truncate(r.body), code)
			}
			return nil
		})
	})
	sc.Step(`^each 429 has a "([^"]*)" header$`, func(name string) error {
		return each429(func(r *reply) error {
			if r.header.Get(name) == "" {
				return fmt.Errorf("a 429 has no %s header", name)
			}
			return nil
		})
	})
	sc.Step(`^each 429 has the header "([^"]*)" with the value "([^"]*)"$`, func(name, value string) error {
		return each429(func(r *reply) error {
			if r.header.Get(name) != value {
				return fmt.Errorf("a 429 has %s %q, want %q", name, r.header.Get(name), value)
			}
			return nil
		})
	})
	sc.Step(`^the usage endpoint's "([^"]*)" is the number of responses that were not 429$`, func(field string) error {
		return w.usageEquals(field, func(r *reply) bool { return r.status != http.StatusTooManyRequests })
	})
	sc.Step(`^the usage endpoint's "([^"]*)" is the number of 429 responses$`, func(field string) error {
		return w.usageEquals(field, func(r *reply) bool { return r.status == http.StatusTooManyRequests })
	})
	sc.Step(`^the management app raises the limit for org "([^"]*)" to (\d+) requests per second$`, func(org string, rps int) error {
		return w.record(func() error {
			return w.app.Directory().Update(tenant.ID(org), func(o *tenant.Org) {
				o.Limits.RequestsPerSecond, o.Limits.Burst = float64(rps), rps
			})
		})
	})
	sc.Step(`^within (\d+) seconds? (\d+) requests in one second are all served$`, func(secs, n int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.eventually(time.Duration(secs)*time.Second, func() error {
			if err := w.sendBurst(n, "GET", "/"+m.orgs[m.defaultOrg].handle+"/hello"); err != nil {
				return err
			}
			for _, r := range w.batch {
				if r.status != http.StatusOK {
					return fmt.Errorf("a request gave %d: %s", r.status, truncate(r.body))
				}
			}
			return nil
		})
	})
	sc.Step(`^the org "([^"]*)" is on Shadow Mock plan "([^"]*)"$`, func(org, plan string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if _, ok := m.orgs[org]; !ok {
			if addErr := w.addOrg(org, org, plan, 0, 0); addErr != nil {
				return addErr
			}
		}
		return w.record(func() error {
			return w.app.Directory().Update(tenant.ID(org), func(o *tenant.Org) { o.Limits = planLimit(plan) })
		})
	})
	_ = strings.TrimSpace
}

// usageEquals checks that a usage measure equals the number of replies in the batch that satisfy count.
func (w *world) usageEquals(field string, count func(*reply) bool) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	buckets, err := w.usageNow(m.defaultOrg)
	if err != nil {
		return err
	}
	var want int64
	for _, r := range w.batch {
		if count(r) {
			want++
		}
	}
	got := usageTotal(buckets, func(b usageBucket) int64 {
		if field == "rateLimited" {
			return b.RateLimited
		}
		return b.Requests
	})
	if got != want {
		return fmt.Errorf("usage %q is %d, want %d", field, got, want)
	}
	return nil
}

func (w *world) initManagedLimits2(sc *godog.ScenarioContext) {
	sc.Step(`^the SUT has used up the org's burst$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.sendBurst(400, "GET", "/"+m.orgs[m.defaultOrg].handle+"/hello")
	})
	sc.Step(`^a request whose body never finishes still gets its 429 at once$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		// Send the headers of a large body and a fragment of it, then wait for the answer on the bare connection.
		u, err := url.Parse(w.srv.URL)
		if err != nil {
			return err
		}
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(context.Background(), "tcp", u.Host)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck // a connection that cannot take a deadline fails the read below
		if _, err = fmt.Fprintf(conn, "POST /%s/hello HTTP/1.1\r\nHost: %s\r\nContent-Type: text/plain\r\nContent-Length: 10000000\r\n\r\nthe start of a body", m.orgs[m.defaultOrg].handle, u.Host); err != nil {
			return err
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			return fmt.Errorf("the engine waited for the request body before answering: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			return fmt.Errorf("the request gave %d, want 429", resp.StatusCode)
		}
		return nil
	})
	sc.Step(`^the stats show the rate-limited requests apart from served requests$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("GET", "/stats/live", "", "", w.setupCred(m.defaultOrg), nil)
		if err != nil {
			return err
		}
		var doc struct {
			RateLimited int `json:"rateLimited"`
		}
		if jsonErr := json.Unmarshal(rep.body, &doc); jsonErr != nil {
			return jsonErr
		}
		want := 0
		for _, r := range w.batch {
			if r.status == http.StatusTooManyRequests {
				want++
			}
		}
		if doc.RateLimited != want {
			return fmt.Errorf("the stats show %d rate-limited requests, want %d", doc.RateLimited, want)
		}
		return nil
	})
}

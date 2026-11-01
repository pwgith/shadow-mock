package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/server"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// harnessPeerHeader lets a step say which TCP peer a request seems to come from. front reads it and removes it, so
// the engine sees the address as it would behind a real connection, and cannot tell the header was there.
const harnessPeerHeader = "X-Harness-Peer"

// proxyAddr is the address of the platform proxy that the harness plays when a step forwards a request.
const proxyAddr = "10.0.0.1"

// countedBody counts the bytes the engine reads from a request body.
type countedBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (c *countedBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// front stands between the test client and the engine. It sets the peer address a step asked for, and in managed
// mode counts what the engine reads of each request body.
func (w *world) front(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if peer := r.Header.Get(harnessPeerHeader); peer != "" {
			r.Header.Del(harnessPeerHeader)
			r.RemoteAddr = net.JoinHostPort(peer, "40000")
		}
		if w.m != nil && r.Body != nil && r.Body != http.NoBody {
			r.Body = &countedBody{ReadCloser: r.Body, n: &w.bodyRead}
		}
		next.ServeHTTP(rw, r)
	})
}

// mockPath makes a path that a scenario wrote for mock traffic reachable in managed mode, where every URL begins with
// the org's handle. A path that already begins with a handle of the scenario, or with a reserved "__" segment, is
// left as it is.
func (w *world) mockPath(p string) string {
	m := w.m
	if m == nil || !strings.HasPrefix(p, "/") {
		return p
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	if strings.HasPrefix(seg, "__") {
		return p
	}
	for _, o := range m.orgs {
		if o.handle == seg {
			return p
		}
	}
	if o, ok := m.orgs[m.defaultOrg]; ok {
		return "/" + o.handle + p
	}
	return p
}

// ensureOrg creates the org as a Team org with its own name as handle, when the scenario has not.
func (w *world) ensureOrg(org string) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	if _, ok := m.orgs[org]; ok {
		return nil
	}
	return w.addOrg(org, org, "Team", 0, 0)
}

// updateOrg changes an org in the engine's directory, as the management app would, and again after a restart.
func (w *world) updateOrg(org string, change func(*tenant.Org)) error {
	return w.record(func() error { return w.app.Directory().Update(tenant.ID(org), change) })
}

// keepLast runs fn and leaves the scenario's last response as it was, for helper calls the scenario did not make.
func (w *world) keepLast(fn func() error) error {
	last, n := w.last, len(w.history)
	err := fn()
	w.last, w.history = last, w.history[:n]
	return err
}

// journalOf reads the org's journal with the harness's own credential.
func (w *world) journalOf(org string) ([]map[string]any, error) {
	var doc struct {
		Requests []map[string]any `json:"requests"`
	}
	err := w.keepLast(func() error {
		rep, err := w.managedAdminCall("GET", "/requests", "", org, w.setupCred(org), nil)
		if err != nil {
			return err
		}
		if rep.status != http.StatusOK {
			return fmt.Errorf("GET /requests gave %d: %s", rep.status, truncate(rep.body))
		}
		return json.Unmarshal(rep.body, &doc)
	})
	return doc.Requests, err
}

// meterField reads one of the org's usage measures, by the name the specs use for it.
func (w *world) meterField(org, name string) (int64, error) {
	var measure func(usageBucket) int64
	switch name {
	case "requests_rejected_ip":
		measure = func(b usageBucket) int64 { return b.IPRejected }
	case "mock_requests":
		measure = func(b usageBucket) int64 { return b.Requests }
	case "requests_rate_limited":
		measure = func(b usageBucket) int64 { return b.RateLimited }
	default:
		return 0, fmt.Errorf("the harness does not know the measure %q", name)
	}
	var total int64
	err := w.keepLast(func() error {
		buckets, err := w.usageNow(org)
		total = usageTotal(buckets, measure)
		return err
	})
	return total, err
}

// clientIPSeen is the address the engine took the last request to come from: the one in a refusal, else the one in
// the journal's newest entry.
func (w *world) clientIPSeen() (string, error) {
	m, err := w.mgd()
	if err != nil {
		return "", err
	}
	if w.last != nil && w.last.status == http.StatusForbidden {
		var doc struct {
			IP      string `json:"ip"`
			Details struct {
				IP string `json:"ip"`
			} `json:"details"`
		}
		if json.Unmarshal(w.last.body, &doc) == nil {
			if ip := doc.IP + doc.Details.IP; ip != "" {
				return ip, nil
			}
		}
	}
	entries, err := w.journalOf(m.defaultOrg)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("the request was refused without an address, and the journal has no entry")
	}
	req, _ := entries[0]["request"].(map[string]any) //nolint:errcheck // a missing request reads as no address
	ip, _ := req["clientIp"].(string)                //nolint:errcheck // see above
	return ip, nil
}

func (w *world) initManagedNetwork(sc *godog.ScenarioContext) {
	// Where the engine reads the client address.
	clientIP := func(header, hops string) error {
		if w.m == nil {
			if err := w.startManaged("management-app"); err != nil {
				return err
			}
		}
		w.m.clientHeader, w.m.clientHops = header, 0
		if hops != "" {
			n, err := strconv.Atoi(hops)
			if err != nil {
				return err
			}
			w.m.clientHops = n
		}
		return w.reconfigureManaged()
	}
	sc.Step(`^Shadow Mock is running in SaaS mode with "server\.clientIp" set to header "([^"]*)"(?: and trustedHops (\d+))?$`, clientIP)

	// The org's allowlist, as the management app writes it.
	sc.Step(`^the org "([^"]*)" IP allowlist is:$`, func(org string, t *godog.Table) error {
		if err := w.ensureOrg(org); err != nil {
			return err
		}
		var cidrs []string
		for _, row := range t.Rows[1:] {
			cidrs = append(cidrs, row.Cells[0].Value)
		}
		prefixes, err := server.ParsePrefixes(cidrs)
		if err != nil {
			return err
		}
		return w.updateOrg(org, func(o *tenant.Org) { o.Allowlist = prefixes })
	})
	sc.Step(`^the org's IP allowlist is "([^"]*)"$`, func(cidr string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		prefixes, err := server.ParsePrefixes([]string{cidr})
		if err != nil {
			return err
		}
		return w.updateOrg(m.defaultOrg, func(o *tenant.Org) { o.Allowlist = prefixes })
	})
	sc.Step(`^the org "([^"]*)" has "applyToAdmin" set to (true|false)$`, func(org, on string) error {
		return w.updateOrg(org, func(o *tenant.Org) { o.AllowlistAdmin = on == "true" })
	})
	sc.Step(`^the org admin adds "([^"]*)" in the management app$`, func(cidr string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		prefixes, err := server.ParsePrefixes([]string{cidr})
		if err != nil {
			return err
		}
		return w.updateOrg(m.defaultOrg, func(o *tenant.Org) { o.Allowlist = append(o.Allowlist, prefixes...) })
	})

	// Mock traffic from an address.
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" via "([^"]*)" from IP "([^"]*)"$`, func(method, p, name, ip string) error {
		r := newRequest(method, w.urlBasedPath(name, p))
		r.header.Set("X-Forwarded-For", ip)
		return w.sendOne(r)
	})
	sc.Step(`^I send a "([^"]*)" request with a (\d+) MB body to "([^"]*)" from IP "([^"]*)"$`, func(method string, mb int, p, ip string) error {
		if err := w.ensure(); err != nil {
			return err
		}
		r := newRequest(method, w.mockPath(p))
		r.header.Set("X-Forwarded-For", ip)
		r.header.Set("Content-Type", "application/octet-stream")
		r.body = make([]byte, mb<<20)
		w.bodyRead.Store(0)
		return w.sendOne(r)
	})
	sc.Step(`^the engine read (\d+) bytes of the request body$`, func(want int) error {
		if got := w.bodyRead.Load(); got != int64(want) {
			return fmt.Errorf("the engine read %d bytes of the request body, want %d", got, want)
		}
		return nil
	})
	sc.Step(`^within (\d+) s a "([A-Z]+)" request to "([^"]*)" from IP "([^"]*)" returns status (\d+)$`, func(secs int, method, p, ip string, want int) error {
		return w.eventually(time.Duration(secs)*time.Second, func() error {
			r := newRequest(method, w.mockPath(p))
			r.header.Set("X-Forwarded-For", ip)
			rep, err := w.send(r)
			if err != nil {
				return err
			}
			if rep.status != want {
				return fmt.Errorf("%s %s from %s gave %d, want %d", method, p, ip, rep.status, want)
			}
			return nil
		})
	})
	sc.Step(`^the SUT sends "([A-Z]+) ([^"]*)" from "([^"]*)"$`, func(method, p, ip string) error {
		r := newRequest(method, w.mockPath(p))
		r.header.Set("X-Forwarded-For", ip)
		return w.sendOne(r)
	})

	// The platform proxy and a client that goes round it.
	sc.Step(`^the proxy simulator forwards a request from real IP "([^"]*)" carrying client header "([^:"]*): ([^"]*)"$`, func(real, name, value string) error {
		r := newRequest("GET", w.mockPath("/x"))
		// What the proxy does: it adds the address it saw to the end of whatever the client sent.
		r.header.Set(name, value+", "+real)
		r.header.Set(harnessPeerHeader, proxyAddr)
		return w.sendOne(r)
	})
	sc.Step(`^a request reaches the engine directly from "([^"]*)" with no X-Forwarded-For header$`, func(peer string) error {
		r := newRequest("GET", w.mockPath("/x"))
		r.header.Set(harnessPeerHeader, peer)
		return w.sendOne(r)
	})
	sc.Step(`^a request arrives with header "([^:"]*): ([^"]*)"$`, func(name, value string) error {
		r := newRequest("GET", w.mockPath("/x"))
		r.header.Set(name, value)
		return w.sendOne(r)
	})
	sc.Step(`^the engine sees client IP "([^"]*)"$`, func(want string) error {
		got, err := w.clientIPSeen()
		if err != nil {
			return err
		}
		if a, parseErr := netip.ParseAddr(got); parseErr == nil {
			got = a.Unmap().String()
		}
		if got != want {
			return fmt.Errorf("the engine saw the client IP %q, want %q", got, want)
		}
		return nil
	})

	// Admin calls from an address.
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" from IP "([^"]*)"$`, func(method, p, ip string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		_, err = w.managedAdminCall(method, p, "", "", m.cred, http.Header{"X-Forwarded-For": {ip}})
		return err
	})
	sc.Step(`^the management app calls admin "([^"]*)" "([^"]*)" with a token it signed, from the host's servers at "([^"]*)"$`, func(method, p, ip string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf("management-app", m.defaultOrg, "admin")
		_, err = w.managedAdminCall(method, p, "", "", &credential{bearer: func() (string, error) { return m.mint(w, sp), nil }}, http.Header{"X-Forwarded-For": {ip}})
		return err
	})
	sc.Step(`^a pipeline calls admin "([^"]*)" "([^"]*)" with a Tool app key from "([^"]*)"$`, func(method, p, ip string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if _, ok := m.keys["pipeline"]; !ok {
			if keyErr := w.addKey("pipeline", m.defaultOrg, []string{"sessions", "read"}); keyErr != nil {
				return keyErr
			}
		}
		_, err = w.managedAdminCall(method, p, "", "", m.keyCred("pipeline"), http.Header{"X-Forwarded-For": {ip}})
		return err
	})

	// What the org's meter and journal show. The engine starts each scenario with nothing counted, so "increases by"
	// is the count at the end of the scenario.
	sc.Step(`^the org "([^"]*)" stat "([^"]*)" increases by (\d+)$`, func(org, name string, by int) error {
		got, err := w.meterField(org, name)
		if err != nil {
			return err
		}
		if got != int64(by) {
			return fmt.Errorf("%s of %s is %d, want %d", name, org, got, by)
		}
		return nil
	})
	sc.Step(`^the org "([^"]*)" meter "([^"]*)" does not increase$`, func(org, name string) error {
		got, err := w.meterField(org, name)
		if err != nil {
			return err
		}
		if got != 0 {
			return fmt.Errorf("%s of %s is %d, want it unchanged at 0", name, org, got)
		}
		return nil
	})
	sc.Step(`^the journal has no entry for that request$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		entries, err := w.journalOf(m.defaultOrg)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return fmt.Errorf("the journal has %d entries, want none", len(entries))
		}
		return nil
	})
}

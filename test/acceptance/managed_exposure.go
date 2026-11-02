package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/server"
)

// what a shared host shows to the public: the routes that need no credential, the metrics, and the way the engine
// reads the client address (FR-9.6, FR-9.6b, NFR-5.2).

// extraYAML is more configuration a scenario has asked for, kept with the managed settings so that a restart keeps it.
func (m *managedWorld) setExtra(w *world, yaml string) error {
	m.extra += yaml + "\n"
	return w.reconfigureManaged()
}

// systemToken is a token the management app signs for its own calls, which is what the metrics ask for.
func (w *world) systemToken() string {
	sp := w.tokenOf("management-app", w.m.defaultOrg, "admin")
	sp.scope = "system"
	return w.m.mint(w, sp)
}

func (w *world) get(path string, header http.Header) error {
	r := newRequest("GET", path)
	for k, vs := range header {
		for _, v := range vs {
			r.header.Add(k, v)
		}
	}
	return w.sendOne(r)
}

func (w *world) initManagedExposure(sc *godog.ScenarioContext) {
	sc.Step(`^I request "([^"]*)" without a credential$`, func(path string) error { return w.get(path, nil) })
	sc.Step(`^I request "([^"]*)" with a valid token$`, func(path string) error {
		if _, err := w.mgd(); err != nil {
			return err
		}
		return w.get(path, http.Header{"Authorization": {"Bearer " + w.systemToken()}})
	})
	sc.Step(`^no metric carries an org handle as a label value$`, func() error {
		// The org of this scenario has the same name and handle, which would hide the difference, so an org with
		// an opaque id and its own handle takes some traffic first.
		const id, handle = "org_01hx7k2", "umbrella-corp"
		if err := w.addOrg(id, handle, "Team", 0, 0); err != nil {
			return err
		}
		if err := w.get("/"+handle+"/hello", nil); err != nil {
			return err
		}
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if err := w.get("/__metrics", http.Header{"Authorization": {"Bearer " + w.systemToken()}}); err != nil {
			return err
		}
		if w.last == nil || w.last.status != http.StatusOK {
			return fmt.Errorf("the metrics were not served: %v", w.last)
		}
		scrape := string(w.last.body)
		if !strings.Contains(scrape, `org="`+id+`"`) {
			return fmt.Errorf("the metrics do not name the org by its id:\n%s", truncate(w.last.body))
		}
		for _, o := range m.orgs {
			if o.handle != o.id && strings.Contains(scrape, o.handle) {
				return fmt.Errorf("the metrics carry the handle %q:\n%s", o.handle, truncate(w.last.body))
			}
		}
		return nil
	})
	sc.Step(`^metrics are served on a private listener$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return m.setExtra(w, "listen: { admin: \"127.0.0.1:9091\" }")
	})
	sc.Step(`^I request "([^"]*)" on the public listener$`, func(path string) error { return w.get(path, nil) })
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" from "([^"]*)" without a credential$`, func(method, p, ip string) error {
		_, err := w.managedAdminCall(method, p, "", "", nil, http.Header{"X-Forwarded-For": {ip}})
		return err
	})

	// How the client address is found.
	sc.Step(`^the number of trusted hops is (\d+)$`, func(n int) error {
		if w.m == nil {
			if err := w.startManaged("management-app"); err != nil {
				return err
			}
		}
		w.m.clientHeader, w.m.clientHops, w.m.clientProxies = "X-Forwarded-For", n, nil
		return w.reconfigureManaged()
	})
	sc.Step(`^the trusted proxies are "([^"]*)"$`, func(cidrs string) error {
		if w.m == nil {
			if err := w.startManaged("management-app"); err != nil {
				return err
			}
		}
		w.m.clientHeader, w.m.clientHops, w.m.clientProxies = "X-Forwarded-For", 0, strings.Split(cidrs, ",")
		return w.reconfigureManaged()
	})
	sc.Step(`^a request arrives with "X-Forwarded-For: ([^"]*)" and the peer "([^"]*)"$`, func(header, peer string) error {
		r := newRequest("GET", w.mockPath("/x"))
		if header != "" {
			r.header.Set("X-Forwarded-For", header)
		}
		r.header.Set(harnessPeerHeader, peer)
		return w.sendOne(r)
	})
	sc.Step(`^a request arrives straight from "([^"]*)" with "X-Forwarded-For: ([^"]*)"$`, func(peer, header string) error {
		r := newRequest("GET", w.mockPath("/x"))
		r.header.Set("X-Forwarded-For", header)
		r.header.Set(harnessPeerHeader, peer)
		return w.sendOne(r)
	})
	sc.Step(`^the client address the engine uses is "([^"]*)"$`, func(want string) error {
		got, err := w.clientIPSeen()
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("the engine used the client address %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^a request arrives with "X-Forwarded-For: ([^"]*)" through one trusted hop$`, func(header string) error {
		r := newRequest("GET", w.mockPath("/x"))
		r.header.Set("X-Forwarded-For", header)
		return w.sendOne(r)
	})
	sc.Step(`^it is allowed$`, func() error {
		if w.last == nil {
			return fmt.Errorf("no response")
		}
		if w.last.status == http.StatusForbidden {
			return fmt.Errorf("the request was refused: %s", truncate(w.last.body))
		}
		return nil
	})
	sc.Step(`^it is rejected with "([^"]*)"$`, func(code string) error {
		if w.last == nil || w.last.status != http.StatusForbidden || !strings.Contains(string(w.last.body), `"`+code+`"`) {
			return fmt.Errorf("the request was not rejected with %s: %v", code, w.last)
		}
		return nil
	})
	sc.Step(`^the environment "([^"]*)" has (\d+) trusted hops? and "([^"]*)" has (\d+)$`, func(a string, na int, b string, nb int) error {
		w.vars["env."+a], w.vars["env."+b] = fmt.Sprint(na), fmt.Sprint(nb)
		return nil
	})
	sc.Step(`^each uses its own count$`, func() error {
		// Each environment sets its count in its own configuration, and the engine counts from the right with it.
		const forwarded = "6.6.6.6, 203.0.113.4, 10.2.2.2"
		want := map[string]string{"staging": "10.2.2.2", "production": "203.0.113.4"}
		for env, client := range want {
			cfg := config.Default()
			if err := config.Merge(&cfg, []byte("server: { clientIp: { header: X-Forwarded-For, trustedHops: "+w.vars["env."+env]+" } }")); err != nil {
				return err
			}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
			if err != nil {
				return err
			}
			req.RemoteAddr = "10.1.1.1:5000"
			req.Header.Set("X-Forwarded-For", forwarded)
			got := server.ClientIP{Header: cfg.Server.ClientIP.Header, TrustedHops: cfg.Server.ClientIP.TrustedHops}.From(req)
			if got.String() != client {
				return fmt.Errorf("%s takes %s as the client, want %s", env, got, client)
			}
		}
		return nil
	})
}

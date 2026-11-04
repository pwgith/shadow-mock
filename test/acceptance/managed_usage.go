package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/server"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// What the engine counts and reports as usage, and how plan limits show in it (FR-11.5, FR-11.12, NFR-4.2).

// freeze stops the test clock where it is, so that a second, an hour or a month passes only when a step says so.
func (w *world) freeze() { w.clk.set(w.clk.Now()) }

// usageOfLast reads the usage buckets from the last response.
func (w *world) usageOfLast() ([]usageBucket, error) {
	if w.last == nil || w.last.status != http.StatusOK {
		return nil, fmt.Errorf("the last response is not a usage answer: %+v", w.last)
	}
	var doc struct {
		Buckets []usageBucket `json:"buckets"`
	}
	if err := json.Unmarshal(w.last.body, &doc); err != nil {
		return nil, fmt.Errorf("parse the usage: %w", err)
	}
	return doc.Buckets, nil
}

// bucketAt finds the bucket that starts at the hour of t in the last response.
func (w *world) bucketAt(t time.Time) (usageBucket, error) {
	bs, err := w.usageOfLast()
	if err != nil {
		return usageBucket{}, err
	}
	h := t.UTC().Truncate(time.Hour)
	for _, b := range bs {
		if b.PeriodStart.Equal(h) {
			return b, nil
		}
	}
	return usageBucket{}, fmt.Errorf("there is no bucket for the hour of %s in %+v", h.Format(time.RFC3339), bs)
}

// noteRequests notes how many requests the org has had counted, before the event a step is about to cause.
func (w *world) noteRequests(org string) error {
	w.freeze()
	got, err := w.meterField(org, "mock_requests")
	if err != nil {
		return err
	}
	w.m.requestsBefore, w.m.requestsNoted = got, true
	return nil
}

// limitOrg changes what an org's plan lets the engine enforce, as the management app would.
func (w *world) limitOrg(org string, change func(*tenant.Limits)) error {
	return w.updateOrg(org, func(o *tenant.Org) { change(&o.Limits) })
}

// allowOnly makes the org's allowlist one range.
func (w *world) allowOnly(org, cidr string) error {
	prefixes, err := server.ParsePrefixes([]string{cidr})
	if err != nil {
		return err
	}
	return w.updateOrg(org, func(o *tenant.Org) { o.Allowlist = prefixes })
}

// adminAs calls admin with a token of a person in an org.
func (w *world) adminAs(method, path, sub, org, role string) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	sp := w.tokenOf(sub, org, role)
	_, err = w.managedAdminCall(method, path, "", org, &credential{org: org, bearer: func() (string, error) { return m.mint(w, sp), nil }}, nil)
	return err
}

func (w *world) initManagedUsage(sc *godog.ScenarioContext) {
	// The org without a plan is on Team.
	sc.Step(`^the org "([^"]*)" with handle "([^"]*)"$`, func(n, h string) error { return w.addOrg(n, h, "Team", 0, 0) })

	// The request rate of a plan: N requests in each of S seconds, the clock standing still within a second.
	sc.Step(`^I send (\d+) requests per second for (\d+) seconds$`, func(rps, secs int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		w.batch, w.batchPaths, m.limitedEachSecond = nil, nil, nil
		for range secs {
			limited := 0
			for range rps {
				rep, sendErr := w.send(newRequest("GET", w.mockPath("/x")))
				if sendErr != nil {
					return sendErr
				}
				w.batch = append(w.batch, rep)
				if rep.status == http.StatusTooManyRequests {
					limited++
				}
			}
			m.limitedEachSecond = append(m.limitedEachSecond, limited)
			w.clk.advance(time.Second)
		}
		return nil
	})
	sc.Step(`^about (\d+) requests per second receive 429 with error "([^"]*)"$`, func(want int, code string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		total := 0
		for _, n := range m.limitedEachSecond {
			total += n
		}
		avg := float64(total) / float64(len(m.limitedEachSecond))
		if tolerance := max(1.0, float64(want)/10); avg < float64(want)-tolerance || avg > float64(want)+tolerance {
			return fmt.Errorf("%.1f requests a second were limited (%v), want about %d", avg, m.limitedEachSecond, want)
		}
		for _, r := range w.batch {
			if r.status == http.StatusTooManyRequests && !strings.Contains(string(r.body), `"`+code+`"`) {
				return fmt.Errorf("a 429 does not carry the error %q: %s", code, truncate(r.body))
			}
		}
		return nil
	})
	sc.Step(`^rate-limited requests are not counted in "([^"]*)"$`, func(name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		var served int64
		for _, r := range w.batch {
			if r.status != http.StatusTooManyRequests {
				served++
			}
		}
		got, err := w.meterField(m.defaultOrg, name)
		if err != nil {
			return err
		}
		if got != served {
			return fmt.Errorf("%s is %d, want the %d requests that were served", name, got, served)
		}
		return nil
	})

	// The monthly cap, and what a plan change does.
	sc.Step(`^org "([^"]*)" has served (\d+) mock requests this month$`, func(org string, n int) error {
		w.freeze()
		w.meterRequests(org, n)
		return nil
	})
	sc.Step(`^the org "([^"]*)" usage counts (\d+) requests this month$`, func(org string, want int) error {
		if got := w.app.Usage().Month(tenant.ID(org), w.clk.Now()); got != int64(want) {
			return fmt.Errorf("%s has %d requests this month, want %d", org, got, want)
		}
		return nil
	})
	sc.Step(`^the platform upgrades org "([^"]*)" to Shadow Mock plan "([^"]*)"$`, func(org, plan string) error {
		return w.limitOrg(org, func(l *tenant.Limits) { *l = planLimit(plan) })
	})
	sc.Step(`^within (\d+) s the engine enforces the ([A-Za-z]+) limits for org "([^"]*)"$`, func(secs int, plan, org string) error {
		lim := planLimit(plan)
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.eventually(time.Duration(secs)*time.Second, func() error {
			w.freeze()
			if err := w.sendBurst(lim.Burst*3/4, "GET", "/"+m.orgs[org].handle+"/x"); err != nil {
				return err
			}
			for _, r := range w.batch {
				if r.status != http.StatusOK {
					return fmt.Errorf("a request of the %s plan's burst gave %d: %s", plan, r.status, truncate(r.body))
				}
			}
			return nil
		})
	})

	// What counts as a billable mock request. Each event notes the count first, so the check can say what changed.
	event := func(do func(m *managedWorld) error) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return do(m)
	}
	mockEvent := func(method, p string) error {
		return event(func(m *managedWorld) error {
			if err := w.noteRequests(m.defaultOrg); err != nil {
				return err
			}
			return w.sendOne(newRequest(method, w.mockPath(p)))
		})
	}
	sc.Step(`^the SUT sends "([A-Z]+) ([^"]*)" and it is served$`, mockEvent)
	sc.Step(`^the SUT sends "([A-Z]+) ([^"]*)" and nothing matches$`, mockEvent)
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with a token$`, func(method, p string) error {
		return event(func(m *managedWorld) error {
			if err := w.noteRequests(m.defaultOrg); err != nil {
				return err
			}
			return w.adminAs(method, p, "alice", m.defaultOrg, "member")
		})
	})
	sc.Step(`^the SUT is rejected by the IP allowlist$`, func() error {
		return event(func(m *managedWorld) error {
			if err := w.allowOnly(m.defaultOrg, "203.0.113.0/24"); err != nil {
				return err
			}
			if err := w.noteRequests(m.defaultOrg); err != nil {
				return err
			}
			r := newRequest("GET", w.mockPath("/hello"))
			r.header.Set("X-Forwarded-For", "198.51.100.7")
			return w.sendOne(r)
		})
	})
	sc.Step(`^the SUT is rate limited$`, func() error {
		return event(func(m *managedWorld) error {
			if err := w.limitOrg(m.defaultOrg, func(l *tenant.Limits) { l.RequestsPerSecond, l.Burst = 1, 1 }); err != nil {
				return err
			}
			w.freeze()
			if err := w.sendOne(newRequest("GET", w.mockPath("/hello"))); err != nil { // uses the burst
				return err
			}
			if err := w.noteRequests(m.defaultOrg); err != nil {
				return err
			}
			return w.sendOne(newRequest("GET", w.mockPath("/hello")))
		})
	})
	sc.Step(`^the SUT is rejected as suspended$`, func() error {
		return event(func(m *managedWorld) error {
			if err := w.noteRequests(m.defaultOrg); err != nil {
				return err
			}
			setStatus := func(s string) error {
				return w.record(func() error { return w.app.Directory().SetStatus(tenant.ID(m.defaultOrg), tenant.Status(s)) })
			}
			if err := setStatus("suspended"); err != nil {
				return err
			}
			if err := w.sendOne(newRequest("GET", w.mockPath("/hello"))); err != nil {
				return err
			}
			if w.last == nil || w.last.status != http.StatusForbidden {
				return fmt.Errorf("a request to a suspended org gave %+v, want 403", w.last)
			}
			// The engine refuses a suspended org's admin calls too, so the management app resumes the org to read its usage.
			return setStatus("active")
		})
	})
	sc.Step(`^the "requests" of the hour (goes up by 1|does not change)$`, func(what string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		if !m.requestsNoted {
			return fmt.Errorf("no step noted the requests before the event")
		}
		bs, err := w.usageOfLast()
		if err != nil {
			return err
		}
		want := m.requestsBefore
		if what == "goes up by 1" {
			want++
		}
		if got := usageTotal(bs, func(b usageBucket) int64 { return b.Requests }); got != want {
			return fmt.Errorf("requests are %d, want %d (%s)", got, want, what)
		}
		return nil
	})

	// Sessions, files and the other measures.
	sc.Step(`^(\d+) sessions are active at (\d+):(\d+) and (\d+) at (\d+):(\d+) and (\d+) at (\d+):(\d+)$`, func(a, ah, am, b, bh, bm, c, ch, cm int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		day := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
		for _, at := range []struct{ n, h, m int }{{a, ah, am}, {b, bh, bm}, {c, ch, cm}} {
			w.clk.set(day.Add(time.Duration(at.h)*time.Hour + time.Duration(at.m)*time.Minute))
			w.app.Usage().Sessions(tenant.ID(m.defaultOrg), at.n)
		}
		return nil
	})
	sc.Step(`^the bucket for (\d+):00 has "([^"]*)" (\d+)$`, func(hour int, field string, want int) error {
		b, err := w.bucketAt(time.Date(2026, 10, 30, hour, 0, 0, 0, time.UTC))
		if err != nil {
			return err
		}
		return bucketField(b, field, want)
	})
	sc.Step(`^(\d+) key-based and (\d+) URL-based shadow mocks are active$`, func(keyBased, urlBased int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		return w.record(func() error {
			for i := range keyBased + urlBased {
				kind := "key-based"
				if i >= keyBased {
					kind = "url-based"
				}
				rep, callErr := w.managedAdminCall("POST", "/sessions/"+kind, "{}", m.defaultOrg, w.setupCred(m.defaultOrg), nil)
				if callErr != nil {
					return callErr
				}
				if rep.status != http.StatusCreated {
					return fmt.Errorf("creating a %s shadow mock gave %d: %s", kind, rep.status, truncate(rep.body))
				}
			}
			return nil
		})
	})
	sc.Step(`^the current bucket has "([^"]*)" (\d+)$`, func(field string, want int) error {
		b, err := w.bucketAt(w.clk.Now())
		if err != nil {
			return err
		}
		return bucketField(b, field, want)
	})
	sc.Step(`^the org stores (\d+) files totalling (\d+) bytes$`, func(n, total int) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		return w.record(func() error {
			scope := store.Global(tenant.ID(m.defaultOrg))
			for i := range n {
				if putErr := w.app.Service().Files.Put(ctxBackground(), scope, fmt.Sprintf("file-%d.bin", i), make([]byte, total/n)); putErr != nil {
					return putErr
				}
			}
			return nil
		})
	})
	sc.Step(`^the current bucket has "storedFiles" with count (\d+) and bytes (\d+)$`, func(count int, bytes int64) error {
		b, err := w.bucketAt(w.clk.Now())
		if err != nil {
			return err
		}
		if b.StoredFiles.Count != count || b.StoredFiles.Bytes != bytes {
			return fmt.Errorf("stored files are %+v, want %d files of %d bytes", b.StoredFiles, count, bytes)
		}
		return nil
	})
	sc.Step(`^the SUT is rate limited (\d+) times and rejected by the allowlist (\d+) times$`, func(limited, rejected int) error {
		return event(func(m *managedWorld) error {
			w.freeze()
			if err := w.limitOrg(m.defaultOrg, func(l *tenant.Limits) { l.RequestsPerSecond, l.Burst = 1, 1 }); err != nil {
				return err
			}
			for range limited + 1 { // the first uses the burst
				if err := w.sendOne(newRequest("GET", w.mockPath("/hello"))); err != nil {
					return err
				}
			}
			if err := w.allowOnly(m.defaultOrg, "203.0.113.0/24"); err != nil {
				return err
			}
			for range rejected {
				r := newRequest("GET", w.mockPath("/hello"))
				r.header.Set("X-Forwarded-For", "198.51.100.7")
				if err := w.sendOne(r); err != nil {
					return err
				}
			}
			return nil
		})
	})
	sc.Step(`^the current bucket has "rateLimited" (\d+) and "ipRejected" (\d+)$`, func(limited, rejected int) error {
		b, err := w.bucketAt(w.clk.Now())
		if err != nil {
			return err
		}
		if b.RateLimited != int64(limited) || b.IPRejected != int64(rejected) {
			return fmt.Errorf("rateLimited = %d and ipRejected = %d, want %d and %d", b.RateLimited, b.IPRejected, limited, rejected)
		}
		return nil
	})

	// Late data is a later revision of the same hour.
	sc.Step(`^a bucket was read at revision 1$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.freeze()
		w.app.Usage().Request(tenant.ID(m.defaultOrg))
		return w.keepLast(func() error {
			bs, readErr := w.usageNow(m.defaultOrg)
			if readErr != nil {
				return readErr
			}
			if len(bs) != 1 || bs[0].Revision != 1 {
				return fmt.Errorf("the first read gave %+v, want one bucket at revision 1", bs)
			}
			m.usageHour = bs[0].PeriodStart
			return nil
		})
	})
	sc.Step(`^late requests for that hour are counted$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		w.app.Usage().Request(tenant.ID(m.defaultOrg))
		return nil
	})
	sc.Step(`^the bucket has the same "periodStart" and a higher "revision"$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		b, err := w.bucketAt(m.usageHour)
		if err != nil {
			return err
		}
		if b.Revision <= 1 {
			return fmt.Errorf("the bucket's revision is %d, want above 1", b.Revision)
		}
		return nil
	})

	// A range, and who may ask.
	sc.Step(`^the response has one bucket for each hour in the range$`, func() error {
		bs, err := w.usageOfLast()
		if err != nil {
			return err
		}
		if len(bs) != 3 {
			return fmt.Errorf("the range 09:00 to 12:00 has %d buckets, want 3", len(bs))
		}
		start := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
		for i, b := range bs {
			if want := start.Add(time.Duration(i) * time.Hour); !b.PeriodStart.Equal(want) {
				return fmt.Errorf("bucket %d starts at %s, want %s", i, b.PeriodStart, want)
			}
		}
		return nil
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with a key that has the scope "([^"]*)"$`, func(method, p, scope string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		label := "scoped-" + scope
		if _, ok := m.keys[label]; !ok {
			if keyErr := w.addKey(label, m.defaultOrg, []string{scope}); keyErr != nil {
				return keyErr
			}
		}
		_, err = w.managedAdminCall(method, p, "", m.defaultOrg, m.keyCred(label), nil)
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with a token that has the scope "([^"]*)"$`, func(method, p, scope string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf("alice", m.defaultOrg, "member")
		sp.scope = scope
		_, err = w.managedAdminCall(method, p, "", m.defaultOrg, &credential{org: m.defaultOrg, bearer: func() (string, error) { return m.mint(w, sp), nil }}, nil)
		return err
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" on org "([^"]*)" with a token for "([^"]*)" in org "([^"]*)"$`, func(method, p, target, sub, own string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf(sub, own, "member")
		_, err = w.managedAdminCall(method, p, "", target, &credential{org: own, bearer: func() (string, error) { return m.mint(w, sp), nil }}, nil)
		return err
	})
}

func bucketField(b usageBucket, field string, want int) error {
	var got int64
	switch field {
	case "requests":
		got = b.Requests
	case "concurrentSessionsMax":
		got = int64(b.ConcurrentSessionsMax)
	case "rateLimited":
		got = b.RateLimited
	case "ipRejected":
		got = b.IPRejected
	default:
		return fmt.Errorf("the harness does not know the usage field %q", field)
	}
	if got != int64(want) {
		return fmt.Errorf("%s is %d, want %d", field, got, want)
	}
	return nil
}

package acceptance

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/cucumber/godog"
)

// initBehaviour registers the steps that check how a response behaves on the wire: how long it
// takes, how it arrives, how it breaks, and whether it is compressed.
func (w *world) initBehaviour(sc *godog.ScenarioContext) {
	sc.Step(`^every response arrives after between (\d+) and (\d+) ms$`, func(lo, hi int) error {
		if len(w.batch) == 0 {
			return fmt.Errorf("no responses were received")
		}
		for i, r := range w.batch {
			if r.elapsed < time.Duration(lo)*time.Millisecond || r.elapsed > time.Duration(hi)*time.Millisecond {
				return fmt.Errorf("response %d took %v, want between %d and %d ms", i+1, r.elapsed, lo, hi)
			}
		}
		return nil
	})
	sc.Step(`^the body arrives in (\d+) chunks over at least (\d+) ms$`, func(chunks, ms int) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if r.chunks != chunks {
			return fmt.Errorf("the body arrived in %d chunks, want %d", r.chunks, chunks)
		}
		if r.elapsed < time.Duration(ms)*time.Millisecond {
			return fmt.Errorf("the body took %v, want at least %d ms", r.elapsed, ms)
		}
		return nil
	})
	sc.Step(`^I send a "([A-Z]+)" request to "([^"]*)" and disconnect after (\d+) ms$`, w.sendAndDisconnect)
	sc.Step(`^within (\d+) ms the engine reports 0 in-flight requests for "([^"]*)"$`, func(ms int, path string) error {
		if !eventually(time.Duration(ms)*time.Millisecond, func() bool { return w.app.Service().InFlight(path) == 0 }) {
			return fmt.Errorf("%d requests for %s are still in flight after %d ms", w.app.Service().InFlight(path), path, ms)
		}
		return nil
	})

	w.initFaultSteps(sc)

	sc.Step(`^the decoded response body has (\d+) characters$`, func(n int) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		body := r.body
		if r.header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("the body is not gzip: %w", err)
			}
			if body, err = io.ReadAll(zr); err != nil {
				return fmt.Errorf("decompress the body: %w", err)
			}
		}
		if len(body) != n {
			return fmt.Errorf("the decoded body has %d characters, want %d", len(body), n)
		}
		return nil
	})
	sc.Step(`^the response JSON at "\$\.nearMisses\[0\]\.matchResult\.distance" is less than the distance for "([^"]*)"$`, func(other string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		first, err := jsonAt(r.body, "$.nearMisses[0].matchResult.distance")
		if err != nil || len(first) != 1 {
			return fmt.Errorf("the first near miss has no distance (%v)", err)
		}
		names, err := jsonAt(r.body, "$.nearMisses[*].stubMapping.name")
		if err != nil {
			return err
		}
		dists, err := jsonAt(r.body, "$.nearMisses[*].matchResult.distance")
		if err != nil || len(names) != len(dists) {
			return fmt.Errorf("near misses have %d names and %d distances (%v)", len(names), len(dists), err)
		}
		for i, n := range names {
			if text(n) != other {
				continue
			}
			a, aok := first[0].(float64)
			b, bok := dists[i].(float64)
			if !aok || !bok || a >= b {
				return fmt.Errorf("the first near miss has distance %v, want less than %v for %q", first[0], dists[i], other)
			}
			return nil
		}
		return fmt.Errorf("no near miss for %q in %s", other, truncate(r.body))
	})
}

// initFaultSteps registers the steps that check how a fault shows up in the client.
func (w *world) initFaultSteps(sc *godog.ScenarioContext) {
	for step, pattern := range map[string]string{
		`the connection is reset`:                                `(?i)reset|forcibly closed|EOF`,
		`the connection closes with no response`:                 `(?i)EOF|reset|forcibly closed`,
		`the response fails with a malformed chunk error`:        `(?i)malformed chunked|invalid byte in chunk|unexpected EOF`,
		`the client receives garbage then the connection closes`: `(?i)malformed HTTP|EOF|reset|forcibly closed`,
	} {
		re := regexp.MustCompile(pattern)
		sc.Step("^"+regexp.QuoteMeta(step)+"$", func() error {
			if w.sendErr == nil {
				return fmt.Errorf("the client received a complete response, want a broken connection")
			}
			if !re.MatchString(w.sendErr.Error()) {
				return fmt.Errorf("the request failed with %q, want an error matching %s", w.sendErr, re)
			}
			return nil
		})
	}
}

// sendAndDisconnect sends a request and drops the connection after a while.
func (w *world) sendAndDisconnect(method, path string, ms int) error {
	if err := w.ensure(); err != nil {
		return err
	}
	target, err := w.resolve(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ms)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := w.client.Do(req)
	if err == nil {
		resp.Body.Close()
		return fmt.Errorf("the response arrived before the client disconnected")
	}
	return nil
}

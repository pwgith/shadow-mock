package acceptance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/cucumber/godog"
)

// maxParallel caps requests in flight at once.
const maxParallel = 64

// initConcurrent registers the steps that drive many sessions at once.
func (w *world) initConcurrent(sc *godog.ScenarioContext) {
	sc.Step(`^each session concurrently sends (\d+) "([A-Z]+)" requests to "([^"]*)"$`, func(n int, method, path string) error {
		if err := w.ensure(); err != nil {
			return err
		}
		return w.sendConcurrently(n, method, path)
	})
	sc.Step(`^every session received exactly the numbers 1 to (\d+)$`, func(n int) error {
		want := make([]int, n)
		for i := range want {
			want[i] = i + 1
		}
		for name, got := range w.sessionNumbers {
			slices.Sort(got)
			if !slices.Equal(got, want) {
				return fmt.Errorf("session %s received %v, want %v", name, got, want)
			}
		}
		if len(w.sessionNumbers) == 0 {
			return fmt.Errorf("no session sent any requests")
		}
		return nil
	})
}

// sendConcurrently sends n requests at once from every session created so far, tagged with
// the session's key, and records the number in each body.
func (w *world) sendConcurrently(n int, method, path string) error {
	type result struct {
		session string
		number  int
		err     error
	}
	var names []string
	for k := range w.vars {
		if len(k) > 4 && k[len(k)-4:] == ".key" {
			names = append(names, k[:len(k)-4])
		}
	}
	slices.Sort(names)
	results := make(chan result, len(names)*n)
	var wg sync.WaitGroup
	slots := make(chan struct{}, maxParallel) // the listen backlog is small on some systems
	for _, name := range names {
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				num, err := w.callWithKey(method, path, w.vars[name+".key"])
				results <- result{name, num, err}
			}()
		}
	}
	wg.Wait()
	close(results)
	w.sessionNumbers = map[string][]int{}
	for r := range results {
		if r.err != nil {
			return fmt.Errorf("%s: %w", r.session, r.err)
		}
		w.sessionNumbers[r.session] = append(w.sessionNumbers[r.session], r.number)
	}
	return nil
}

// callWithKey sends one request carrying a session key and parses the body as a number.
func (w *world) callWithKey(method, path, key string) (int, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, w.srv.URL+path, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Shadow-Mock-Key", key)
	resp, err := w.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}
	n, err := strconv.Atoi(string(body))
	if err != nil {
		return 0, fmt.Errorf("body %q is not a number (status %d)", body, resp.StatusCode)
	}
	return n, nil
}

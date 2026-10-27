// Package acceptance runs the Gherkin specs in features/ against the engine, in process.
//
// Each scenario gets its own engine, root directory and test clock. The engine is wired by
// internal/app exactly as cmd/shadowmock wires it, and is reached over real HTTP through an
// httptest server, so the specs exercise routing, headers and bodies as a client sees them.
//
// The suite covers the engine in standalone mode (phases P0 to P2). Scenarios for SaaS,
// multi-instance runs, later phases and work not built yet are excluded by tag; see
// DefaultTags and features/README.md.
package acceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/pkg/extension"
)

// testClock is the engine's clock. Until a scenario sets the time it follows the real clock;
// after that it stands still and moves only when the scenario advances it. Sleeping always
// waits for real time, so delay assertions measure real delays (features/README.md).
type testClock struct {
	mu     sync.Mutex
	frozen bool
	base   time.Time
	offset time.Duration
}

// Now implements clock.Clock.
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return c.base
	}
	return time.Now().Add(c.offset)
}

// Sleep implements clock.Clock; it waits for real time.
func (c *testClock) Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *testClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frozen, c.base = true, t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		c.base = c.base.Add(d)
		return
	}
	c.offset += d
}

// syncBuffer collects the engine's log output.
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

// world is the state of one scenario.
type world struct {
	root string
	clk  *testClock
	logs *syncBuffer

	// Engine configuration: YAML documents merged over the defaults, in order.
	yamls []string
	watch bool

	app      *app.App
	srv      *httptest.Server
	cancel   context.CancelFunc
	stopOnce sync.Once
	stopped  chan struct{}

	// replay re-applies the scenario's setup steps after the engine is reconfigured, because
	// a changed configuration needs a new engine (for example "running with configuration").
	replay []func() error

	vars   map[string]string
	client *http.Client

	last    *reply
	batch   []*reply
	history []*reply

	// state for individual steps
	receivers      map[string]*receiver
	upstreams      map[string]*upstream
	matchers       []extension.RequestMatcher
	sessionNumbers map[string][]int

	createdSession map[string]any
	marks          map[string]time.Time
	sessionCounts  map[string]int
	genKeys        []string
	genKind        string
	httpClientBase string

	// configuration scenarios (Shadow Mock starts)
	cfgDocs  []string
	cfgKV    [][2]string
	cfgEnv   map[string]string
	cfgFlags []string
	cfgOut   config.Config
	cfgErr   error
}

func newWorld() (*world, error) {
	root, err := os.MkdirTemp("", "shadowmock-acceptance-")
	if err != nil {
		return nil, fmt.Errorf("create scenario root: %w", err)
	}
	return &world{
		root:          root,
		clk:           &testClock{},
		logs:          &syncBuffer{},
		stopped:       make(chan struct{}),
		vars:          map[string]string{},
		marks:         map[string]time.Time{},
		sessionCounts: map[string]int{},
		cfgEnv:        map[string]string{},
		client: &http.Client{
			Timeout:       60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// start builds and starts an engine from the scenario's configuration.
func (w *world) start() error {
	cfg := config.Default()
	cfg.Root = w.root
	cfg.Storage.Global.Watch = w.watch
	for _, y := range w.yamls {
		if err := config.Merge(&cfg, []byte(y)); err != nil {
			return fmt.Errorf("configuration: %w", err)
		}
	}
	logger := slog.New(slog.NewTextHandler(w.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, err := app.New(cfg, app.Options{Logger: logger, Clock: w.clk, Version: "acceptance", Shutdown: w.markStopped, Matchers: w.matchers})
	if err != nil {
		return fmt.Errorf("build engine: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		cancel()
		return errors.Join(fmt.Errorf("start engine: %w", err), a.Close())
	}
	w.app, w.cancel, w.srv = a, cancel, httptest.NewServer(a.Handler())
	w.stopOnce = sync.Once{}
	w.stopped = make(chan struct{})
	return nil
}

func (w *world) markStopped() { w.stopOnce.Do(func() { close(w.stopped) }) }

// stop shuts the engine down. The root directory is kept.
func (w *world) stop() error {
	if w.app == nil {
		return nil
	}
	w.srv.Close()
	w.cancel()
	err := w.app.Close()
	w.app, w.srv = nil, nil
	return err
}

// close ends the scenario and removes its files.
func (w *world) close() error {
	w.closeReceivers()
	w.closeUpstreams()
	err := w.stop()
	return errors.Join(err, os.RemoveAll(w.root))
}

// ensure starts the default engine if no step has started one yet.
func (w *world) ensure() error {
	if w.app != nil {
		return nil
	}
	return w.startWithReplay()
}

func (w *world) startWithReplay() error {
	if err := w.start(); err != nil {
		return err
	}
	for _, fn := range w.replay {
		if err := fn(); err != nil {
			return fmt.Errorf("re-apply setup after restart: %w", err)
		}
	}
	return nil
}

// record runs a setup step now and again after any reconfiguration.
func (w *world) record(fn func() error) error {
	if err := w.ensure(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	w.replay = append(w.replay, fn)
	return nil
}

// reconfigure merges more YAML into the configuration and restarts the engine, keeping the
// scenario's setup.
func (w *world) reconfigure(yaml string) error {
	if err := w.stop(); err != nil {
		return err
	}
	w.yamls = append(w.yamls, yaml)
	return w.startWithReplay()
}

var varPattern = regexp.MustCompile(`\$\{([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)?)\}`)

// sub replaces ${NAME} for known variables, and the {mock base} and {admin base} shorthands.
// Unknown ${…} text, such as ${json-unit.ignore}, is left alone.
func (w *world) sub(s string) string {
	s = varPattern.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := w.vars[m[2:len(m)-1]]; ok {
			return v
		}
		return m
	})
	if w.srv != nil {
		s = strings.ReplaceAll(s, "{mock base}", w.srv.URL)
		s = strings.ReplaceAll(s, "{admin base}", w.srv.URL+w.adminPrefix())
	}
	return s
}

func (w *world) adminPrefix() string {
	cfg := config.Default()
	for _, y := range w.yamls {
		_ = config.Merge(&cfg, []byte(y)) //nolint:errcheck // already validated when the engine started
	}
	return cfg.AdminPrefix
}

func (w *world) logContains(parts ...string) bool {
	for _, line := range strings.Split(w.logs.String(), "\n") {
		ok := line != ""
		for _, p := range parts {
			ok = ok && strings.Contains(line, p)
		}
		if ok {
			return true
		}
	}
	return false
}

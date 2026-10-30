package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/config"
)

// initEnv registers the environment, clock and configuration steps.
func (w *world) initEnv(sc *godog.ScenarioContext) {
	sc.Step(`^Shadow Mock is running$`, w.ensure)
	sc.Step(`^Shadow Mock is running in standalone mode$`, w.ensure)
	sc.Step(`^Shadow Mock is running with default storage$`, w.ensure)
	sc.Step(`^Shadow Mock is running with configuration:$`, func(doc *godog.DocString) error { return w.reconfigure(doc.Content) })
	sc.Step(`^Shadow Mock is running with root "([^"]*)"$`, func(string) error { return w.restart(false) })
	sc.Step(`^Shadow Mock is started with root "([^"]*)"$`, func(string) error { return w.restart(false) })
	sc.Step(`^Shadow Mock is running with root "([^"]*)" and hot reload "(enabled|disabled)"$`, func(_, mode string) error {
		w.watch = mode == "enabled"
		return w.restart(false)
	})
	sc.Step(`^Shadow Mock is running with ambiguity policy "([^"]*)"$`, func(p string) error {
		return w.reconfigure("sessions: { detect: { ambiguity: " + p + " } }")
	})
	sc.Step(`^Shadow Mock is listening on http port (\d+) and https port (\d+)$`, func(_, https int) error {
		return w.reconfigure("tls: { port: " + strconv.Itoa(https) + " }")
	})
	sc.Step(`^Shadow Mock restarts$`, func() error { return w.restart(true) })
	sc.Step(`^the mock root directory "([^"]*)" is empty$`, func(string) error { return clearDir(w.root) })
	sc.Step(`^I am not authenticated$`, func() error { return nil }) // standalone always authenticates as local

	sc.Step(`^the test clock is at "([^"]*)"$`, func(s string) error {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return fmt.Errorf("parse time %q: %w", s, err)
		}
		w.clk.set(t)
		return nil
	})
	sc.Step(`^the test clock advances by "([^"]*)"$`, w.advanceClock)
	sc.Step(`^I wait (\d+) ms$`, func(ms int) error { time.Sleep(time.Duration(ms) * time.Millisecond); return nil })
	sc.Step(`^within (\d+) s the server has stopped$`, func(s int) error {
		select {
		case <-w.stopped:
			return nil
		case <-time.After(time.Duration(s) * time.Second):
			return fmt.Errorf("the server had not stopped after %d s", s)
		}
	})

	sc.Step(`^the log contains an error mentioning "([^"]*)"(?: and "([^"]*)")?$`, func(a, b string) error {
		if !w.logContains("level=ERROR", a, b) {
			return fmt.Errorf("no error log line mentions %q and %q; logs:\n%s", a, b, w.logs.String())
		}
		return nil
	})
	sc.Step(`^start-up succeeds with no warnings for "([^"]*)"$`, func(file string) error {
		if w.logContains("level=WARN", file) || w.logContains("level=ERROR", file) {
			return fmt.Errorf("start-up logged a problem with %s:\n%s", file, w.logs.String())
		}
		return nil
	})

	w.initConfigSteps(sc)
}

func (w *world) advanceClock(spec string) error {
	d, err := time.ParseDuration(spec)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", spec, err)
	}
	w.clk.advance(d)
	if w.app != nil {
		w.app.Service().ReapOnce(context.Background()) // the reaper ticks in real time; run it now
	}
	return nil
}

// restart stops the engine and starts it again over the same root. With fresh, the setup
// steps are not re-applied: whatever the engine persisted is all it has.
func (w *world) restart(fresh bool) error {
	if err := w.stop(); err != nil {
		return err
	}
	if fresh {
		w.replay = nil
		return w.start()
	}
	return w.startWithReplay()
}

func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("clear %s: %w", e.Name(), err)
		}
	}
	return nil
}

// initConfigSteps registers the steps that resolve configuration from file, environment and
// flags without starting an engine.
func (w *world) initConfigSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the configuration file sets "([^"]*)" to "([^"]*)"$`, func(key, val string) error {
		w.cfgKV = append(w.cfgKV, [2]string{key, val})
		return nil
	})
	sc.Step(`^the configuration file contains:$`, func(doc *godog.DocString) error {
		w.cfgDocs = append(w.cfgDocs, doc.Content)
		return nil
	})
	sc.Step(`^the environment variable "([^"]*)" is "([^"]*)"$`, func(k, v string) error {
		if v != "" {
			w.cfgEnv[k] = v
		}
		return nil
	})
	sc.Step(`^the command-line flag "([^"]*)" is "([^"]*)"$`, func(flag, v string) error {
		if v != "" {
			w.cfgFlags = append(w.cfgFlags, flag, v)
		}
		return nil
	})
	sc.Step(`^the command-line flags are "([^"]*)"$`, func(args string) error {
		w.cfgFlags = append(w.cfgFlags, strings.Fields(args)...)
		return nil
	})
	sc.Step(`^Shadow Mock starts$`, func() error {
		path := filepath.Join(w.root, "shadowmock.yaml")
		if err := os.WriteFile(path, []byte(w.configYAML()), 0o600); err != nil {
			return fmt.Errorf("write configuration file: %w", err)
		}
		args := append([]string{"--config", path}, w.cfgFlags...)
		w.cfgOut, _, w.cfgErr = config.Resolve(args, func(k string) (string, bool) { v, ok := w.cfgEnv[k]; return v, ok })
		return nil
	})
	sc.Step(`^it listens for mock traffic on "([^"]*)"$`, func(want string) error {
		if w.cfgErr != nil {
			return fmt.Errorf("start-up failed: %w", w.cfgErr)
		}
		if w.cfgOut.Listen.Mock != want {
			return fmt.Errorf("listen.mock = %q, want %q", w.cfgOut.Listen.Mock, want)
		}
		return nil
	})
	sc.Step(`^start-up fails with an error mentioning "([^"]*)"$`, func(part string) error {
		if w.cfgErr == nil {
			return errors.New("start-up succeeded, want a failure")
		}
		if !strings.Contains(w.cfgErr.Error(), part) {
			return fmt.Errorf("start-up error %q does not mention %q", w.cfgErr, part)
		}
		return nil
	})
}

// configYAML renders the scenario's configuration file: documents first, then dotted keys.
func (w *world) configYAML() string {
	var b strings.Builder
	for _, d := range w.cfgDocs {
		b.WriteString(d + "\n")
	}
	for _, kv := range w.cfgKV {
		parts := strings.Split(kv[0], ".")
		for i, p := range parts[:len(parts)-1] {
			b.WriteString(strings.Repeat("  ", i) + p + ":\n")
		}
		fmt.Fprintf(&b, "%s%s: %q\n", strings.Repeat("  ", len(parts)-1), parts[len(parts)-1], kv[1])
	}
	return b.String()
}

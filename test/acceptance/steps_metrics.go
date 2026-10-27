package acceptance

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

// metricLabels are the only label names a metric may carry (CODING_STANDARDS.md §11). The le
// label belongs to histogram buckets.
var metricLabels = map[string]bool{"org": true, "layer": true, "stage": true, "status": true, "method": true, "le": true}

var (
	seriesLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})?\s+\S+`)
	labelPair  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)
)

// initMetrics registers the steps that read the Prometheus metrics.
func (w *world) initMetrics(sc *godog.ScenarioContext) {
	sc.Step(`^I scrape "([^"]*)"$`, func(path string) error {
		rep, err := w.send(newRequest("GET", path))
		if err != nil {
			return err
		}
		if rep.status != 200 {
			return fmt.Errorf("scraping %s returned %d: %s", path, rep.status, truncate(rep.body))
		}
		w.scrape = string(rep.body)
		return nil
	})
	sc.Step(`^the metrics include:$`, func(t *godog.Table) error {
		for _, row := range t.Rows[1:] {
			if err := w.hasSeries(w.sub(row.Cells[0].Value)); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^no metric has a label for session id, path or client IP$`, func() error {
		for _, line := range strings.Split(w.scrape, "\n") {
			m := seriesLine.FindStringSubmatch(line)
			if m == nil || strings.HasPrefix(line, "#") {
				continue
			}
			for _, p := range labelPair.FindAllStringSubmatch(m[2], -1) {
				if !metricLabels[p[1]] {
					return fmt.Errorf("metric %s has the label %q, which is not allowed", m[1], p[1])
				}
			}
		}
		for _, secret := range []string{"ses_", "smk_", "127.0.0.1"} {
			if strings.Contains(w.scrape, secret) {
				return fmt.Errorf("the metrics mention %q, which identifies a session or a client", secret)
			}
		}
		return nil
	})
	sc.Step(`^the metrics have no series for "([^"]*)"$`, func(name string) error {
		if err := w.hasSeries(name); err == nil {
			return fmt.Errorf("the metrics have a series for %s, want none", name)
		}
		return nil
	})
	sc.Step(`^the metric "([^"]*)" has a value of at least (\d+)$`, func(name string, atLeast int) error {
		for _, line := range strings.Split(w.scrape, "\n") {
			var v float64
			if strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{") {
				fields := strings.Fields(line)
				if _, err := fmt.Sscan(fields[len(fields)-1], &v); err == nil && v >= float64(atLeast) {
					return nil
				}
			}
		}
		return fmt.Errorf("no series of %s has a value of at least %d in:\n%s", name, atLeast, truncate([]byte(w.scrape)))
	})
}

// hasSeries checks that the scrape has a series matching spec: a metric name, optionally with
// label matchers such as {layer="..."}, where "..." stands for any value. A histogram or summary
// counts when any of its _bucket, _sum or _count series is present.
func (w *world) hasSeries(spec string) error {
	name, want, _ := strings.Cut(spec, "{")
	want = strings.TrimSuffix(want, "}")
	wantPairs := labelPair.FindAllStringSubmatch(want, -1)
	for _, line := range strings.Split(w.scrape, "\n") {
		m := seriesLine.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(line, "#") {
			continue
		}
		if m[1] != name && !strings.HasPrefix(m[1], name+"_") {
			continue
		}
		have := map[string]string{}
		for _, p := range labelPair.FindAllStringSubmatch(m[2], -1) {
			have[p[1]] = p[2]
		}
		ok := true
		for _, p := range wantPairs {
			if v, present := have[p[1]]; !present || (p[2] != "..." && v != p[2]) {
				ok = false
			}
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("the metrics have no series for %s in:\n%s", spec, truncate([]byte(w.scrape)))
}

package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/metrics"
)

func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/__metrics", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape = %d", rec.Code)
	}
	return rec.Body.String()
}

func TestRequestsAreCountedByOrgMethodAndStatus(t *testing.T) {
	t.Parallel()
	m := metrics.New(func() int { return 0 })
	m.Request("local", "GET", 200, "global", "")
	m.Request("local", "GET", 200, "global", "")
	m.Request("local", "POST", 404, "", "")
	out := scrape(t, m)
	for _, want := range []string{
		`shadowmock_requests_total{method="GET",org="local",status="200"} 2`,
		`shadowmock_requests_total{method="POST",org="local",status="404"} 1`,
		`shadowmock_matches_total{layer="global",org="local"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

func TestUnmatchedAndUndetectedRequestsAddNoMatchOrDetectionSeries(t *testing.T) {
	t.Parallel()
	m := metrics.New(func() int { return 0 })
	m.Request("local", "GET", 404, "", "")
	out := scrape(t, m)
	if strings.Contains(out, "shadowmock_matches_total{") || strings.Contains(out, "shadowmock_detection_hits_total{") {
		t.Errorf("an unmatched request with no session should add no match or detection series:\n%s", out)
	}
}

func TestDetectionHitsAreCountedByStage(t *testing.T) {
	t.Parallel()
	m := metrics.New(func() int { return 0 })
	m.Request("local", "GET", 200, "key-based", "Header")
	m.Request("local", "GET", 200, "key-based", "ScanBody")
	out := scrape(t, m)
	for _, want := range []string{`stage="Header"`, `stage="ScanBody"`, `layer="key-based"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

func TestDetectorRebuildsAreReported(t *testing.T) {
	t.Parallel()
	m := metrics.New(func() int { return 0 })
	m.Rebuilt(7, 3*time.Millisecond)
	out := scrape(t, m)
	for _, want := range []string{"shadowmock_automaton_patterns 7", "shadowmock_automaton_rebuild_seconds_count 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestActiveSessionsIsReadWhenScraped(t *testing.T) {
	t.Parallel()
	n := 2
	m := metrics.New(func() int { return n })
	if !strings.Contains(scrape(t, m), "shadowmock_active_sessions 2") {
		t.Error("the gauge should show 2")
	}
	n = 5
	if !strings.Contains(scrape(t, m), "shadowmock_active_sessions 5") {
		t.Error("the gauge should follow the session count at scrape time")
	}
}

// A label whose values are unbounded, or that identifies a person or a session, would grow the
// metrics without limit and leak identifiers to whoever can scrape them.
func TestOnlyAllowedLabelNamesAppear(t *testing.T) {
	t.Parallel()
	m := metrics.New(func() int { return 1 })
	m.Request("local", "GET", 200, "key-based", "Header")
	m.Rebuilt(1, time.Millisecond)
	allowed := map[string]bool{"org": true, "layer": true, "stage": true, "status": true, "method": true, "le": true}
	for _, name := range regexp.MustCompile(`([a-z_]+)="`).FindAllStringSubmatch(scrape(t, m), -1) {
		if !allowed[name[1]] {
			t.Errorf("label %q is not allowed", name[1])
		}
	}
}

func TestSeparateInstancesDoNotShareState(t *testing.T) {
	t.Parallel()
	a, b := metrics.New(func() int { return 0 }), metrics.New(func() int { return 0 })
	a.Request("local", "GET", 200, "", "")
	if strings.Contains(scrape(t, b), "shadowmock_requests_total{") {
		t.Error("two engines in one process must not share counters")
	}
}

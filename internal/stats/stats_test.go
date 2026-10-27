package stats_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/stats"
)

var now = time.Date(2026, 10, 27, 12, 30, 45, 0, time.UTC)

func event(ago time.Duration, layer, path string, latency time.Duration, stage string) stats.Event {
	return stats.Event{At: now.Add(-ago), Tenant: "acme", Layer: layer, Path: path, Latency: latency, Stage: stage}
}

func TestLive(t *testing.T) {
	t.Parallel()
	tr := stats.New()
	for _, e := range []stats.Event{
		event(10*time.Second, "global", "/a", 2*time.Millisecond, ""),
		event(20*time.Second, "key-based", "/a", 4*time.Millisecond, "Header"),
		event(30*time.Second, "key-based", "/b", 6*time.Millisecond, "ScanBody"),
		event(40*time.Second, "", "/missing", 8*time.Millisecond, ""),
		event(50*time.Second, "", "/missing", 10*time.Millisecond, ""),
		event(2*time.Minute, "", "/other", 12*time.Millisecond, ""),
		event(10*time.Minute, "global", "/old", 100*time.Millisecond, ""), // outside a 5 m window
	} {
		tr.Record(e)
	}
	tr.Record(stats.Event{At: now, Tenant: "globex", Layer: "global", Path: "/elsewhere"}) // another org
	tr.RecordIPRejection("acme", "203.0.113.7")
	tr.RecordIPRejection("acme", "203.0.113.7")
	tr.RecordIPRejection("acme", "198.51.100.1")

	got := tr.Live("acme", "5m", 5*time.Minute, now, stats.ActiveSessions{KeyBased: 2, URLBased: 1})

	if got.Window != "5m" || got.ActiveSessions != (stats.ActiveSessions{KeyBased: 2, URLBased: 1}) {
		t.Errorf("window %q, active %+v", got.Window, got.ActiveSessions)
	}
	if got.Unmatched != 3 {
		t.Errorf("Unmatched = %d, want 3", got.Unmatched)
	}
	if want := 3.0 / 6.0; got.MatchRate != want {
		t.Errorf("MatchRate = %v, want %v", got.MatchRate, want)
	}
	if diff := cmp.Diff([]stats.PathCount{{Path: "/missing", Count: 2}, {Path: "/other", Count: 1}}, got.TopUnmatchedPaths); diff != "" {
		t.Errorf("TopUnmatchedPaths (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]int{"Header": 1, "ScanBody": 1}, got.DetectionHits); diff != "" {
		t.Errorf("DetectionHits (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]stats.IPCount{{IP: "203.0.113.7", Count: 2}, {IP: "198.51.100.1", Count: 1}}, got.IPRejections); diff != "" {
		t.Errorf("IPRejections (-want +got):\n%s", diff)
	}
	if got.LatencyMs.P50 != 8 || got.LatencyMs.P99 != 12 {
		t.Errorf("latency p50 = %v, p99 = %v; want 8 and 12", got.LatencyMs.P50, got.LatencyMs.P99)
	}

	// Unmatched requests count towards the global layer in the per-minute series.
	total := 0
	for i, m := range got.RequestsPerMinute {
		total += m.Count
		if i > 0 && m.Minute.Before(got.RequestsPerMinute[i-1].Minute) {
			t.Errorf("RequestsPerMinute is not in time order: %v", got.RequestsPerMinute)
		}
	}
	if total != 6 {
		t.Errorf("RequestsPerMinute adds up to %d, want 6", total)
	}
}

func TestLiveWithNothingRecorded(t *testing.T) {
	t.Parallel()
	got := stats.New().Live("acme", "15m", 15*time.Minute, now, stats.ActiveSessions{})
	if got.MatchRate != 0 || got.Unmatched != 0 || got.LatencyMs != (stats.Latency{}) {
		t.Errorf("empty Live() = %+v", got)
	}
	// The lists are empty rather than null, so the API returns [] and not null.
	if got.RequestsPerMinute == nil || got.TopUnmatchedPaths == nil || got.IPRejections == nil || got.DetectionHits == nil {
		t.Errorf("empty collections must not be nil: %+v", got)
	}
}

func TestRecordForgetsOldHistory(t *testing.T) {
	t.Parallel()
	tr := stats.New()
	tr.Record(event(3*time.Hour, "global", "/old", time.Millisecond, ""))
	tr.Record(event(0, "global", "/new", time.Millisecond, ""))
	// A window as long as the retained history still sees only the new event.
	got := tr.Live("acme", "1h", 24*time.Hour, now, stats.ActiveSessions{})
	count := 0
	for _, m := range got.RequestsPerMinute {
		count += m.Count
	}
	if count != 1 {
		t.Errorf("history holds %d events, want only the one within the last hour", count)
	}
}

func TestTopUnmatchedPathsAreCappedAndTied(t *testing.T) {
	t.Parallel()
	tr := stats.New()
	for _, p := range []string{"/k", "/j", "/i", "/h", "/g", "/f", "/e", "/d", "/c", "/b", "/a"} {
		tr.Record(event(time.Second, "", p, time.Millisecond, ""))
	}
	got := tr.Live("acme", "5m", 5*time.Minute, now, stats.ActiveSessions{}).TopUnmatchedPaths
	if len(got) != 10 || got[0].Path != "/a" || got[9].Path != "/j" {
		t.Errorf("TopUnmatchedPaths = %v, want 10 entries ordered by path when counts tie", got)
	}
}

// Package stats keeps the live per-org counters behind /__admin/stats/live: requests per
// minute by layer, match rate, top unmatched paths, latency and detection hits.
package stats

import (
	"sort"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

// keep is how much history is held; the longest window the API offers is 1 h.
const keep = time.Hour

// Event is one served request.
type Event struct {
	At      time.Time
	Tenant  tenant.ID
	Layer   string // key-based, url-based, global, or "" when unmatched
	Path    string
	Latency time.Duration
	Stage   string // detection stage, or ""
}

// Tracker records events in memory.
type Tracker struct {
	mu     sync.Mutex
	events map[tenant.ID][]Event
	ipRej  map[tenant.ID]map[string]int
}

// New returns an empty tracker.
func New() *Tracker {
	return &Tracker{events: map[tenant.ID][]Event{}, ipRej: map[tenant.ID]map[string]int{}}
}

// Record adds an event and drops history older than an hour.
func (t *Tracker) Record(e Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	evs := t.events[e.Tenant]
	evs = append(evs, e)
	cut := 0
	for cut < len(evs) && e.At.Sub(evs[cut].At) > keep {
		cut++
	}
	t.events[e.Tenant] = evs[cut:]
}

// RecordIPRejection counts a request refused by the IP allowlist.
func (t *Tracker) RecordIPRejection(org tenant.ID, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ipRej[org] == nil {
		t.ipRej[org] = map[string]int{}
	}
	t.ipRej[org][ip]++
}

// Minute is a per-minute, per-layer count.
type Minute struct {
	Minute time.Time `json:"minute"`
	Layer  string    `json:"layer"`
	Count  int       `json:"count"`
}

// PathCount is an unmatched path and how often it was requested.
type PathCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// IPCount is a rejected client IP.
type IPCount struct {
	IP    string `json:"ip"`
	Count int    `json:"count"`
}

// Latency holds latency percentiles in milliseconds.
type Latency struct {
	P50 float64 `json:"p50"`
	P99 float64 `json:"p99"`
}

// ActiveSessions counts active sessions by kind.
type ActiveSessions struct {
	KeyBased int `json:"keyBased"`
	URLBased int `json:"urlBased"`
}

// Live is the LiveStats response.
type Live struct {
	Window            string         `json:"window"`
	RequestsPerMinute []Minute       `json:"requestsPerMinute"`
	MatchRate         float64        `json:"matchRate"`
	Unmatched         int            `json:"unmatched"`
	TopUnmatchedPaths []PathCount    `json:"topUnmatchedPaths"`
	LatencyMs         Latency        `json:"latencyMs"`
	DetectionHits     map[string]int `json:"detectionHits"`
	ActiveSessions    ActiveSessions `json:"activeSessions"`
	IPRejections      []IPCount      `json:"ipRejections"`
}

// Live summarises the org's events in the window ending at now.
func (t *Tracker) Live(org tenant.ID, window string, d time.Duration, now time.Time, active ActiveSessions) Live {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := Live{
		Window: window, DetectionHits: map[string]int{}, ActiveSessions: active,
		RequestsPerMinute: []Minute{}, TopUnmatchedPaths: []PathCount{}, IPRejections: []IPCount{},
	}
	perMinute := map[Minute]int{}
	unmatchedPaths := map[string]int{}
	var latencies []float64
	total := 0
	for _, e := range t.events[org] {
		if now.Sub(e.At) > d {
			continue
		}
		total++
		layer := e.Layer
		if layer == "" {
			out.Unmatched++
			unmatchedPaths[e.Path]++
			layer = "global"
		}
		perMinute[Minute{Minute: e.At.UTC().Truncate(time.Minute), Layer: layer}]++
		latencies = append(latencies, float64(e.Latency)/float64(time.Millisecond))
		if e.Stage != "" {
			out.DetectionHits[e.Stage]++
		}
	}
	for m, n := range perMinute {
		m.Count = n
		out.RequestsPerMinute = append(out.RequestsPerMinute, m)
	}
	sort.Slice(out.RequestsPerMinute, func(i, j int) bool {
		a, b := out.RequestsPerMinute[i], out.RequestsPerMinute[j]
		if !a.Minute.Equal(b.Minute) {
			return a.Minute.Before(b.Minute)
		}
		return a.Layer < b.Layer
	})
	if total > 0 {
		out.MatchRate = float64(total-out.Unmatched) / float64(total)
	}
	out.TopUnmatchedPaths = top(unmatchedPaths, 10)
	out.LatencyMs = percentiles(latencies)
	for ip, n := range t.ipRej[org] {
		out.IPRejections = append(out.IPRejections, IPCount{IP: ip, Count: n})
	}
	sort.Slice(out.IPRejections, func(i, j int) bool { return out.IPRejections[i].Count > out.IPRejections[j].Count })
	return out
}

func top(m map[string]int, n int) []PathCount {
	out := make([]PathCount, 0, len(m))
	for p, c := range m {
		out = append(out, PathCount{Path: p, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func percentiles(xs []float64) Latency {
	if len(xs) == 0 {
		return Latency{}
	}
	sort.Float64s(xs)
	at := func(p float64) float64 { return xs[min(int(p*float64(len(xs))), len(xs)-1)] }
	return Latency{P50: at(0.50), P99: at(0.99)}
}

// Package usage counts what an org has used, in hourly buckets, for GET /__admin/usage and for the
// monthly cap (FR-11.5, FR-11.12). The management app reads the buckets and reports them to the
// platform; the engine reports nothing.
package usage

import (
	"sort"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Files is what an org has stored.
type Files struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// Bucket is one org's usage in one hour.
type Bucket struct {
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	// Requests are served mock requests: not admin calls, IP rejections, rate-limited or refused ones.
	Requests int64 `json:"requests"`
	// ConcurrentSessionsMax is the highest number of shadow mocks active at once in the hour.
	ConcurrentSessionsMax int `json:"concurrentSessionsMax"`
	// StoredFiles is the most the org stored in the hour.
	StoredFiles Files `json:"storedFiles"`
	RateLimited int64 `json:"rateLimited"`
	IPRejected  int64 `json:"ipRejected"`
	// Revision rises each time the bucket is read after it changed, so a reader can tell late data.
	Revision int `json:"revision"`
}

type bucket struct {
	Bucket
	read  bool
	dirty bool
}

// Meter holds the buckets in memory.
type Meter struct {
	clk clock.Clock

	mu   sync.Mutex
	orgs map[tenant.ID]map[time.Time]*bucket
}

// New returns an empty meter.
func New(clk clock.Clock) *Meter {
	return &Meter{clk: clk, orgs: map[tenant.ID]map[time.Time]*bucket{}}
}

func hourOf(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }

// at returns the bucket for the hour of t. The caller holds the lock.
func (m *Meter) at(org tenant.ID, t time.Time) *bucket {
	h := hourOf(t)
	hours := m.orgs[org]
	if hours == nil {
		hours = map[time.Time]*bucket{}
		m.orgs[org] = hours
	}
	b := hours[h]
	if b == nil {
		b = &bucket{Bucket: Bucket{PeriodStart: h, PeriodEnd: h.Add(time.Hour)}}
		hours[h] = b
	}
	b.dirty = true
	return b
}

// Request counts a served mock request.
func (m *Meter) Request(org tenant.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.at(org, m.clk.Now()).Requests++
}

// RateLimited counts a request refused by the rate limit.
func (m *Meter) RateLimited(org tenant.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.at(org, m.clk.Now()).RateLimited++
}

// IPRejected counts a request refused by the allowlist.
func (m *Meter) IPRejected(org tenant.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.at(org, m.clk.Now()).IPRejected++
}

// Sessions notes how many shadow mocks are active now.
func (m *Meter) Sessions(org tenant.ID, active int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b := m.at(org, m.clk.Now()); active > b.ConcurrentSessionsMax {
		b.ConcurrentSessionsMax = active
	}
}

// StoredFiles notes how many files and bytes the org stores now.
func (m *Meter) StoredFiles(org tenant.ID, f Files) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.at(org, m.clk.Now())
	b.StoredFiles.Count = max(b.StoredFiles.Count, f.Count)
	b.StoredFiles.Bytes = max(b.StoredFiles.Bytes, f.Bytes)
}

// Month is how many requests the org has had served in the calendar month of t.
func (m *Meter) Month(org tenant.ID, t time.Time) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	t = t.UTC()
	var n int64
	for h, b := range m.orgs[org] {
		if h.Year() == t.Year() && h.Month() == t.Month() {
			n += b.Requests
		}
	}
	return n
}

// Buckets returns the org's hourly buckets that start in [from, to), oldest first. A zero `to` means now.
func (m *Meter) Buckets(org tenant.ID, from, to time.Time) []Bucket {
	m.mu.Lock()
	defer m.mu.Unlock()
	if to.IsZero() {
		to = m.clk.Now().Add(time.Hour)
	}
	out := []Bucket{}
	for h, b := range m.orgs[org] {
		if h.Before(hourOf(from)) || !h.Before(to) {
			continue
		}
		if !b.read {
			b.Revision = 1
		} else if b.dirty {
			b.Revision++
		}
		b.read, b.dirty = true, false
		out = append(out, b.Bucket)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeriodStart.Before(out[j].PeriodStart) })
	return out
}

// Forget drops everything held for the org.
func (m *Meter) Forget(org tenant.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.orgs, org)
}

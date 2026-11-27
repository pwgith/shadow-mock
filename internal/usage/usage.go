// Package usage counts what an org has used, in hourly buckets, for GET /__admin/usage and for the
// monthly cap (FR-11.5, FR-11.12). The management app reads the buckets and reports them to the
// platform; the engine reports nothing.
package usage

import (
	"context"
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

// Meter holds the buckets in memory, and with a Store also writes them beyond the instance (store.go).
type Meter struct {
	clk   clock.Clock
	store Store

	mu   sync.Mutex
	orgs map[tenant.ID]map[time.Time]*bucket
	// pending is what this instance counted since it last wrote to the store, by org and hour.
	pending map[tenant.ID]map[time.Time]*Bucket
	// base is each org's stored requests in baseMonth, as read at the last write.
	base      map[tenant.ID]int64
	baseMonth time.Time
}

// New returns an empty meter.
func New(clk clock.Clock) *Meter {
	return &Meter{clk: clk, orgs: map[tenant.ID]map[time.Time]*bucket{}, pending: map[tenant.ID]map[time.Time]*Bucket{}}
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

// count applies fn to the org's bucket for this hour, and to what is waiting to be written when there is a store.
func (m *Meter) count(org tenant.ID, fn func(*Bucket)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clk.Now()
	fn(&m.at(org, now).Bucket)
	if m.store != nil {
		fn(m.pendingAt(org, hourOf(now)))
	}
}

// Request counts a served mock request.
func (m *Meter) Request(org tenant.ID) { m.count(org, func(b *Bucket) { b.Requests++ }) }

// RateLimited counts a request refused by the rate limit.
func (m *Meter) RateLimited(org tenant.ID) { m.count(org, func(b *Bucket) { b.RateLimited++ }) }

// IPRejected counts a request refused by the allowlist.
func (m *Meter) IPRejected(org tenant.ID) { m.count(org, func(b *Bucket) { b.IPRejected++ }) }

// Sessions notes how many shadow mocks are active now.
func (m *Meter) Sessions(org tenant.ID, active int) {
	m.count(org, func(b *Bucket) { b.ConcurrentSessionsMax = max(b.ConcurrentSessionsMax, active) })
}

// StoredFiles notes how many files and bytes the org stores now.
func (m *Meter) StoredFiles(org tenant.ID, f Files) {
	m.count(org, func(b *Bucket) {
		b.StoredFiles.Count = max(b.StoredFiles.Count, f.Count)
		b.StoredFiles.Bytes = max(b.StoredFiles.Bytes, f.Bytes)
	})
}

// Month is how many requests the org has had served in the calendar month of t. With a store it is what every
// instance has written, as read at this instance's last write, plus what this one has not written yet.
func (m *Meter) Month(org tenant.ID, t time.Time) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil {
		return m.storedMonth(org, t)
	}
	t = t.UTC()
	var n int64
	for h, b := range m.orgs[org] {
		if h.Year() == t.Year() && h.Month() == t.Month() {
			n += b.Requests
		}
	}
	return n
}

// Buckets returns the org's hourly buckets that start in [from, to), oldest first. A zero `to` means now. When both
// ends are given, every hour of the range has a bucket, and an hour with nothing counted is an empty one.
func (m *Meter) Buckets(org tenant.ID, from, to time.Time) []Bucket {
	return m.BucketsContext(context.Background(), org, from, to)
}

// BucketsContext is Buckets for a caller with a context, which bounds the read of the store.
func (m *Meter) BucketsContext(ctx context.Context, org tenant.ID, from, to time.Time) []Bucket {
	fill := !from.IsZero() && !to.IsZero()
	if to.IsZero() {
		to = m.clk.Now().Add(time.Hour)
	}
	if m.store != nil {
		if stored, ok := m.storedBuckets(ctx, org, from, to); ok {
			return withEveryHour(stored, from, to, fill)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
	return withEveryHour(out, from, to, fill)
}

// withEveryHour adds an empty bucket for each hour of [from, to) that has none when fill is set, and sorts the
// buckets oldest first.
func withEveryHour(out []Bucket, from, to time.Time, fill bool) []Bucket {
	if fill {
		have := make(map[time.Time]bool, len(out))
		for _, b := range out {
			have[b.PeriodStart] = true
		}
		for h := hourOf(from); h.Before(to); h = h.Add(time.Hour) {
			if !have[h] {
				out = append(out, Bucket{PeriodStart: h, PeriodEnd: h.Add(time.Hour)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeriodStart.Before(out[j].PeriodStart) })
	return out
}

// Forget drops everything held for the org.
func (m *Meter) Forget(org tenant.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.orgs, org)
	delete(m.pending, org)
	delete(m.base, org)
}

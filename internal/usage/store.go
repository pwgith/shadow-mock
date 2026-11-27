package usage

import (
	"context"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Store keeps the meter's hourly counts beyond the instance (FR-11.12). Each instance adds what it counted since
// its last write, so restarts, deploys and several instances add up to one total, and the monthly cap still holds
// after a restart.
type Store interface {
	// Add adds each delta's counts to the stored bucket of its org and hour, and keeps the higher of each peak.
	Add(ctx context.Context, deltas []Delta) error
	// Load returns the org's stored buckets that start in [from, to).
	Load(ctx context.Context, org tenant.ID, from, to time.Time) ([]Bucket, error)
	// MonthRequests returns each org's stored requests in the calendar month that starts at month.
	MonthRequests(ctx context.Context, month time.Time) (map[tenant.ID]int64, error)
}

// Delta is what one instance counted in one org's hour since it last wrote to the store.
type Delta struct {
	Org tenant.ID
	Bucket
}

// storeTimeout bounds one read of the store for the admin API.
const storeTimeout = 5 * time.Second

// NewStored returns a meter that also writes to the store. Call Flush once before serving, so that the monthly
// cap starts from what is stored, and then regularly and on shutdown.
func NewStored(clk clock.Clock, st Store) *Meter {
	m := New(clk)
	m.store = st
	return m
}

func monthOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// pendingAt returns the delta waiting to be written for the org and hour. The caller holds the lock.
func (m *Meter) pendingAt(org tenant.ID, hour time.Time) *Bucket {
	hours := m.pending[org]
	if hours == nil {
		hours = map[time.Time]*Bucket{}
		m.pending[org] = hours
	}
	b := hours[hour]
	if b == nil {
		b = &Bucket{PeriodStart: hour, PeriodEnd: hour.Add(time.Hour)}
		hours[hour] = b
	}
	return b
}

// merge adds a delta's counts to b and keeps the higher of each peak.
func merge(b, d *Bucket) {
	b.Requests += d.Requests
	b.RateLimited += d.RateLimited
	b.IPRejected += d.IPRejected
	b.ConcurrentSessionsMax = max(b.ConcurrentSessionsMax, d.ConcurrentSessionsMax)
	b.StoredFiles.Count = max(b.StoredFiles.Count, d.StoredFiles.Count)
	b.StoredFiles.Bytes = max(b.StoredFiles.Bytes, d.StoredFiles.Bytes)
}

// Flush writes what was counted since the last write, then reads the month's stored totals for the monthly cap.
// When the write fails the counts are kept for the next one, so nothing is lost while the database is away.
func (m *Meter) Flush(ctx context.Context) error {
	if m.store == nil {
		return nil
	}
	m.mu.Lock()
	taken := m.pending
	m.pending = map[tenant.ID]map[time.Time]*Bucket{}
	m.mu.Unlock()
	var deltas []Delta
	for org, hours := range taken {
		for _, b := range hours {
			deltas = append(deltas, Delta{Org: org, Bucket: *b})
		}
	}
	if err := m.store.Add(ctx, deltas); err != nil {
		m.mu.Lock()
		for org, hours := range taken {
			for h, b := range hours {
				merge(m.pendingAt(org, h), b)
			}
		}
		m.mu.Unlock()
		return err
	}
	month := monthOf(m.clk.Now())
	totals, err := m.store.MonthRequests(ctx, month)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.base, m.baseMonth = totals, month
	m.mu.Unlock()
	return nil
}

// storedMonth is Month with a store. The caller holds the lock.
func (m *Meter) storedMonth(org tenant.ID, t time.Time) int64 {
	month := monthOf(t)
	var n int64
	if month.Equal(m.baseMonth) {
		n = m.base[org]
	}
	for h, b := range m.pending[org] {
		if monthOf(h).Equal(month) {
			n += b.Requests
		}
	}
	return n
}

// storedBuckets is the org's stored buckets in [from, to) with what has not been written yet added. It answers false
// when the store cannot be read, and the caller falls back to what this instance counted.
func (m *Meter) storedBuckets(ctx context.Context, org tenant.ID, from, to time.Time) ([]Bucket, bool) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	stored, err := m.store.Load(ctx, org, hourOf(from), to)
	if err != nil {
		return nil, false
	}
	byHour := make(map[time.Time]*Bucket, len(stored))
	for i := range stored {
		stored[i].Revision = 1
		byHour[stored[i].PeriodStart] = &stored[i]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, d := range m.pending[org] {
		if h.Before(hourOf(from)) || !h.Before(to) {
			continue
		}
		if b := byHour[h]; b != nil {
			merge(b, d)
			continue
		}
		b := *d
		b.Revision = 1
		stored = append(stored, b)
	}
	return stored, true
}

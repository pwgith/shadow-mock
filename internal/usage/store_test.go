package usage_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/tenant"
	"github.com/pwgith/shadow-mock/internal/usage"
)

// memStore is a Store in memory that adds deltas the way the database does.
type memStore struct {
	mu      sync.Mutex
	rows    map[tenant.ID]map[time.Time]usage.Bucket
	failAdd bool
	adds    int
}

func newMemStore() *memStore { return &memStore{rows: map[tenant.ID]map[time.Time]usage.Bucket{}} }

func (s *memStore) Add(_ context.Context, deltas []usage.Delta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAdd {
		return errors.New("database away")
	}
	s.adds++
	for _, d := range deltas {
		if s.rows[d.Org] == nil {
			s.rows[d.Org] = map[time.Time]usage.Bucket{}
		}
		b := s.rows[d.Org][d.PeriodStart]
		b.PeriodStart, b.PeriodEnd = d.PeriodStart, d.PeriodEnd
		b.Requests += d.Requests
		b.RateLimited += d.RateLimited
		b.IPRejected += d.IPRejected
		b.ConcurrentSessionsMax = max(b.ConcurrentSessionsMax, d.ConcurrentSessionsMax)
		b.StoredFiles.Count = max(b.StoredFiles.Count, d.StoredFiles.Count)
		b.StoredFiles.Bytes = max(b.StoredFiles.Bytes, d.StoredFiles.Bytes)
		s.rows[d.Org][d.PeriodStart] = b
	}
	return nil
}

func (s *memStore) Load(_ context.Context, org tenant.ID, from, to time.Time) ([]usage.Bucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []usage.Bucket{}
	for h, b := range s.rows[org] {
		if !h.Before(from) && h.Before(to) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *memStore) MonthRequests(_ context.Context, month time.Time) (map[tenant.ID]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[tenant.ID]int64{}
	for org, hours := range s.rows {
		for h, b := range hours {
			if !h.Before(month) && h.Before(month.AddDate(0, 1, 0)) {
				out[org] += b.Requests
			}
		}
	}
	return out, nil
}

func storedMeter(st usage.Store) (*usage.Meter, *clock.Fake) {
	clk := clock.NewFake(time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC))
	return usage.NewStored(clk, st), clk
}

func TestCountsSurviveARestart(t *testing.T) {
	t.Parallel()
	st := newMemStore()
	first, _ := storedMeter(st)
	for range 3 {
		first.Request("acme")
	}
	first.Sessions("acme", 4)
	if err := first.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	// A new instance, as after a deploy or a sleeping instance waking: it starts from what is stored.
	second, _ := storedMeter(st)
	if err := second.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	second.Request("acme")
	if got := second.Month("acme", time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)); got != 4 {
		t.Errorf("month = %d, want the 3 stored and the 1 not yet written", got)
	}
	b := second.Buckets("acme", time.Time{}, time.Time{})
	if len(b) != 1 || b[0].Requests != 4 || b[0].ConcurrentSessionsMax != 4 {
		t.Errorf("buckets = %+v, want one hour with 4 requests and the stored peak of 4", b)
	}
}

func TestTwoInstancesAddUp(t *testing.T) {
	t.Parallel()
	st := newMemStore()
	a, _ := storedMeter(st)
	b, _ := storedMeter(st)
	a.Request("acme")
	b.Request("acme")
	b.Request("acme")
	a.Sessions("acme", 2)
	b.Sessions("acme", 5)
	for _, m := range []*usage.Meter{a, b} {
		if err := m.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := a.Month("acme", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)); got != 3 {
		t.Errorf("month = %d, want 3 from both instances", got)
	}
	got := a.Buckets("acme", time.Time{}, time.Time{})[0]
	if got.Requests != 3 || got.ConcurrentSessionsMax != 5 {
		t.Errorf("bucket = %+v, want 3 requests and the higher peak, 5", got)
	}
}

func TestAFailedWriteKeepsTheCounts(t *testing.T) {
	t.Parallel()
	st := newMemStore()
	m, _ := storedMeter(st)
	m.Request("acme")
	st.failAdd = true
	if err := m.Flush(t.Context()); err == nil {
		t.Fatal("want the write to fail")
	}
	m.Request("acme")
	st.failAdd = false
	if err := m.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MonthRequests(t.Context(), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); got["acme"] != 2 {
		t.Errorf("stored = %d, want both requests once the database is back", got["acme"])
	}
	if got := m.Month("acme", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)); got != 2 {
		t.Errorf("month = %d, want 2 and nothing counted twice", got)
	}
}

func TestStoredMonthIsTheCalendarMonthOnly(t *testing.T) {
	t.Parallel()
	st := newMemStore()
	m, clk := storedMeter(st)
	m.Request("acme")
	if err := m.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	clk.Set(time.Date(2026, 12, 1, 0, 30, 0, 0, time.UTC))
	m.Request("acme")
	if err := m.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := m.Month("acme", clk.Now()); got != 1 {
		t.Errorf("December = %d, want only December's request", got)
	}
	if got := m.Month("acme", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)); got != 0 {
		t.Errorf("November as read in December = %d, want 0: only the current month is kept for the cap", got)
	}
}

func TestStoredBucketsFillTheRangeAndForgetDropsPending(t *testing.T) {
	t.Parallel()
	st := newMemStore()
	m, _ := storedMeter(st)
	m.Request("acme")
	from := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	got := m.Buckets("acme", from, from.Add(3*time.Hour))
	if len(got) != 3 || got[1].Requests != 1 || got[0].Requests != 0 {
		t.Errorf("buckets = %+v, want three hours with the request in the second", got)
	}
	m.Forget("acme")
	if err := m.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if st.adds != 1 || len(st.rows["acme"]) != 0 {
		t.Errorf("a forgotten org's unwritten counts were written: %+v", st.rows)
	}
}

func TestAMeterWithoutAStoreHasNothingToFlush(t *testing.T) {
	t.Parallel()
	m, _ := newMeter()
	if err := m.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

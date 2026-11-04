package usage_test

import (
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/usage"
)

func newMeter() (*usage.Meter, *clock.Fake) {
	clk := clock.NewFake(time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC))
	return usage.New(clk), clk
}

func TestCountsFallInTheHourTheyHappened(t *testing.T) {
	t.Parallel()
	m, clk := newMeter()
	m.Request("acme")
	m.Request("acme")
	m.RateLimited("acme")
	m.IPRejected("acme")
	clk.Advance(time.Hour)
	m.Request("acme")
	m.Request("globex")

	got := m.Buckets("acme", time.Time{}, time.Time{})
	if len(got) != 2 {
		t.Fatalf("acme has %d buckets, want 2: %+v", len(got), got)
	}
	first, second := got[0], got[1]
	if !first.PeriodStart.Equal(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)) || !first.PeriodEnd.Equal(first.PeriodStart.Add(time.Hour)) {
		t.Errorf("first bucket spans %v to %v", first.PeriodStart, first.PeriodEnd)
	}
	if first.Requests != 2 || first.RateLimited != 1 || first.IPRejected != 1 || second.Requests != 1 {
		t.Errorf("counts = %+v and %+v", first, second)
	}
}

func TestSessionsAndFilesKeepTheirPeak(t *testing.T) {
	t.Parallel()
	m, _ := newMeter()
	m.Sessions("acme", 3)
	m.Sessions("acme", 1)
	m.StoredFiles("acme", usage.Files{Count: 5, Bytes: 900})
	m.StoredFiles("acme", usage.Files{Count: 2, Bytes: 1200})

	b := m.Buckets("acme", time.Time{}, time.Time{})[0]
	if b.ConcurrentSessionsMax != 3 {
		t.Errorf("concurrent sessions = %d, want the peak of 3", b.ConcurrentSessionsMax)
	}
	if b.StoredFiles != (usage.Files{Count: 5, Bytes: 1200}) {
		t.Errorf("stored files = %+v, want the most of each, 5 and 1200", b.StoredFiles)
	}
}

func TestMonthSumsTheCalendarMonthOnly(t *testing.T) {
	t.Parallel()
	m, clk := newMeter()
	m.Request("acme")
	clk.Advance(24 * time.Hour) // 3 Nov
	m.Request("acme")
	m.Request("acme")
	clk.Advance(30 * 24 * time.Hour) // 3 Dec
	m.Request("acme")

	if n := m.Month("acme", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)); n != 3 {
		t.Errorf("November = %d, want 3", n)
	}
	if n := m.Month("acme", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); n != 1 {
		t.Errorf("December = %d, want 1", n)
	}
	if n := m.Month("nobody", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); n != 0 {
		t.Errorf("an org with no usage = %d, want 0", n)
	}
}

func TestBucketsAreSelectedByRangeAndRevisedWhenReadAfterChange(t *testing.T) {
	t.Parallel()
	m, clk := newMeter()
	m.Request("acme")
	hour1 := clk.Now()
	clk.Advance(time.Hour)
	m.Request("acme")
	clk.Advance(time.Hour)
	m.Request("acme")

	tenAM := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	if got := m.Buckets("acme", tenAM, tenAM.Add(time.Hour)); len(got) != 1 || !got[0].PeriodStart.Equal(tenAM) {
		t.Errorf("range [10:00, 11:00) = %+v, want the 10:00 bucket", got)
	}
	// The first read of a bucket is revision 1, a read with no change keeps it, and a read after a change raises it.
	first := m.Buckets("acme", hour1, time.Time{})
	if first[0].Revision != 1 {
		t.Errorf("first read revision = %d, want 1", first[0].Revision)
	}
	if again := m.Buckets("acme", hour1, time.Time{}); again[0].Revision != 1 {
		t.Errorf("an unchanged read revision = %d, want 1", again[0].Revision)
	}
	m.Request("acme") // late data into the current hour
	if late := m.Buckets("acme", hour1, time.Time{}); late[len(late)-1].Revision != 2 {
		t.Errorf("a read after a change has revision %d, want 2", late[len(late)-1].Revision)
	}
}

func TestARangeHasABucketForEveryHourOfIt(t *testing.T) {
	t.Parallel()
	m, clk := newMeter()
	m.Request("acme") // 09:00
	clk.Advance(2 * time.Hour)
	m.Request("acme") // 11:00

	nine := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	got := m.Buckets("acme", nine, nine.Add(4*time.Hour))
	if len(got) != 4 {
		t.Fatalf("a range of four hours has %d buckets: %+v", len(got), got)
	}
	for i, b := range got {
		if want := nine.Add(time.Duration(i) * time.Hour); !b.PeriodStart.Equal(want) || !b.PeriodEnd.Equal(want.Add(time.Hour)) {
			t.Errorf("bucket %d spans %v to %v, want the hour from %v", i, b.PeriodStart, b.PeriodEnd, want)
		}
	}
	if got[0].Requests != 1 || got[1].Requests != 0 || got[2].Requests != 1 || got[3].Requests != 0 {
		t.Errorf("requests = %d %d %d %d, want 1 0 1 0", got[0].Requests, got[1].Requests, got[2].Requests, got[3].Requests)
	}
	// Without an end the answer holds only the hours that have something.
	if open := m.Buckets("acme", nine, time.Time{}); len(open) != 2 {
		t.Errorf("an open range has %d buckets, want the 2 with counts", len(open))
	}
}

func TestForgetDropsTheOrg(t *testing.T) {
	t.Parallel()
	m, _ := newMeter()
	m.Request("acme")
	m.Request("globex")
	m.Forget("acme")
	if got := m.Buckets("acme", time.Time{}, time.Time{}); len(got) != 0 {
		t.Errorf("a forgotten org has %d buckets", len(got))
	}
	if got := m.Buckets("globex", time.Time{}, time.Time{}); len(got) != 1 {
		t.Errorf("another org has %d buckets, want 1", len(got))
	}
}

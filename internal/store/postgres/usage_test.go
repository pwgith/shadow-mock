package postgres_test

import (
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/store/postgres/pgtest"
	"github.com/pwgith/shadow-mock/internal/usage"
)

func TestUsageAddsCountsAndKeepsPeaks(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	org, _ := newOrg(t, db, "active")
	st := db.Usage()
	hour := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	next := hour.Add(time.Hour)
	delta := func(at time.Time, requests int64, sessions int, files usage.Files) usage.Delta {
		return usage.Delta{Org: org, Bucket: usage.Bucket{
			PeriodStart: at, PeriodEnd: at.Add(time.Hour), Requests: requests, ConcurrentSessionsMax: sessions,
			StoredFiles: files, RateLimited: 1, IPRejected: 2,
		}}
	}
	// Two instances, or one instance twice, write the same hour.
	if err := st.Add(t.Context(), []usage.Delta{delta(hour, 3, 2, usage.Files{Count: 4, Bytes: 100})}); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(t.Context(), []usage.Delta{
		delta(hour, 5, 1, usage.Files{Count: 2, Bytes: 300}),
		delta(next, 7, 0, usage.Files{}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	got, err := st.Load(t.Context(), org, hour, next.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []usage.Bucket{
		{
			PeriodStart: hour, PeriodEnd: next, Requests: 8, ConcurrentSessionsMax: 2,
			StoredFiles: usage.Files{Count: 4, Bytes: 300}, RateLimited: 2, IPRejected: 4,
		},
		{PeriodStart: next, PeriodEnd: next.Add(time.Hour), Requests: 7, RateLimited: 1, IPRejected: 2},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("stored = %+v, want %+v", got, want)
	}

	month, err := st.MonthRequests(t.Context(), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if month[org] != 15 {
		t.Errorf("November = %d, want 15", month[org])
	}
	december, err := st.MonthRequests(t.Context(), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if december[org] != 0 {
		t.Errorf("December = %d, want 0", december[org])
	}
}

package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pwgith/shadow-mock/internal/tenant"
	"github.com/pwgith/shadow-mock/internal/usage"
)

// Usage keeps the meter's hourly counts in engine.usage, so that they outlive the instance (FR-11.12).
type Usage struct{ db *DB }

var _ usage.Store = (*Usage)(nil)

// Usage returns the usage store.
func (d *DB) Usage() *Usage { return &Usage{db: d} }

// Add implements usage.Store: each bucket's counts are added to what is stored, and its peaks kept when higher.
func (u *Usage) Add(ctx context.Context, deltas []usage.Delta) error {
	if len(deltas) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, d := range deltas {
		b.Queue(`INSERT INTO engine.usage AS u (org_id, period_start, requests, concurrent_sessions_max, stored_files,
				stored_bytes, rate_limited, ip_rejected)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (org_id, period_start) DO UPDATE SET
				requests = u.requests + excluded.requests,
				concurrent_sessions_max = greatest(u.concurrent_sessions_max, excluded.concurrent_sessions_max),
				stored_files = greatest(u.stored_files, excluded.stored_files),
				stored_bytes = greatest(u.stored_bytes, excluded.stored_bytes),
				rate_limited = u.rate_limited + excluded.rate_limited,
				ip_rejected = u.ip_rejected + excluded.ip_rejected,
				updated_at = now()`,
			string(d.Org), d.PeriodStart.UTC(), d.Requests, d.ConcurrentSessionsMax, d.StoredFiles.Count,
			d.StoredFiles.Bytes, d.RateLimited, d.IPRejected)
	}
	return u.db.pool.SendBatch(ctx, b).Close()
}

// Load implements usage.Store: the org's stored buckets that start in [from, to).
func (u *Usage) Load(ctx context.Context, org tenant.ID, from, to time.Time) ([]usage.Bucket, error) {
	rows, err := u.db.pool.Query(ctx, `SELECT period_start, requests, concurrent_sessions_max, stored_files, stored_bytes,
			rate_limited, ip_rejected
		FROM engine.usage WHERE org_id = $1 AND period_start >= $2 AND period_start < $3 ORDER BY period_start`,
		string(org), from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []usage.Bucket{}
	for rows.Next() {
		var b usage.Bucket
		if err := rows.Scan(&b.PeriodStart, &b.Requests, &b.ConcurrentSessionsMax, &b.StoredFiles.Count,
			&b.StoredFiles.Bytes, &b.RateLimited, &b.IPRejected); err != nil {
			return nil, err
		}
		b.PeriodStart = b.PeriodStart.UTC()
		b.PeriodEnd = b.PeriodStart.Add(time.Hour)
		out = append(out, b)
	}
	return out, rows.Err()
}

// MonthRequests implements usage.Store: each org's stored requests in the calendar month that starts at month.
func (u *Usage) MonthRequests(ctx context.Context, month time.Time) (map[tenant.ID]int64, error) {
	rows, err := u.db.pool.Query(ctx, `SELECT org_id, sum(requests)::bigint FROM engine.usage
		WHERE period_start >= $1 AND period_start < $2 GROUP BY org_id`, month.UTC(), month.UTC().AddDate(0, 1, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[tenant.ID]int64{}
	for rows.Next() {
		var org string
		var n int64
		if err := rows.Scan(&org, &n); err != nil {
			return nil, err
		}
		out[tenant.ID(org)] = n
	}
	return out, rows.Err()
}

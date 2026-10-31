package postgres

import (
	"context"
	"time"

	"github.com/pwgith/shadow-mock/internal/audit"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// auditTimeout bounds a write to the audit log. A slow database must not hold up the admin call being audited
// for long, and the failure is reported, never swallowed.
const auditTimeout = 3 * time.Second

// maxAuditList bounds what one list call returns.
const maxAuditList = 10_000

// Audit is the audit trail in the database, so that it outlives the instance (FR-11.15).
type Audit struct {
	db      *DB
	onError func(error)
}

var _ audit.Store = (*Audit)(nil)

// Audit returns the trail. onError, when set, is told when a write fails.
func (d *DB) Audit(onError func(error)) *Audit { return &Audit{db: d, onError: onError} }

// Record implements audit.Store.
func (a *Audit) Record(ctx context.Context, e audit.Entry) {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	_, err := a.db.pool.Exec(ctx, `INSERT INTO engine.audit_log (at, org_id, issuer, subject, key_label, token_id, method, route, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.At, string(e.Org), e.Issuer, e.Subject, e.Key, e.TokenID, e.Method, e.Route, e.Status)
	if err != nil && a.onError != nil {
		a.onError(err)
	}
}

// List implements audit.Store: the org's entries, newest first.
func (a *Audit) List(ctx context.Context, org tenant.ID) []audit.Entry {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	rows, err := a.db.pool.Query(ctx, `SELECT at, issuer, subject, key_label, token_id, method, route, status
		FROM engine.audit_log WHERE org_id = $1 ORDER BY id DESC LIMIT $2`, string(org), maxAuditList)
	if err != nil {
		a.fail(err)
		return []audit.Entry{}
	}
	defer rows.Close()
	out := []audit.Entry{}
	for rows.Next() {
		e := audit.Entry{Org: org}
		if err = rows.Scan(&e.At, &e.Issuer, &e.Subject, &e.Key, &e.TokenID, &e.Method, &e.Route, &e.Status); err != nil {
			a.fail(err)
			break
		}
		e.At = e.At.UTC()
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		a.fail(err)
	}
	return out
}

// Anonymise implements audit.Store.
func (a *Audit) Anonymise(ctx context.Context, org tenant.ID, person string) int {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	tag, err := a.db.pool.Exec(ctx, "UPDATE engine.audit_log SET subject = $3 WHERE org_id = $1 AND subject = $2", string(org), person, audit.Anonymous)
	if err != nil {
		a.fail(err)
		return 0
	}
	return int(tag.RowsAffected())
}

func (a *Audit) fail(err error) {
	if a.onError != nil {
		a.onError(err)
	}
}

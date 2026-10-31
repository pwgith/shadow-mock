package postgres

import (
	"context"
	"fmt"
	"net/netip"
	"sync/atomic"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Orgs is the directory of orgs, read from the database. A lookup is served from a snapshot in memory, because
// every request asks for its org; Refresh rebuilds the snapshot, and the engine calls it when a change notice
// arrives and on a slow poll.
type Orgs struct {
	db      *DB
	snap    atomic.Pointer[tenant.Memory]
	onError func(handle string, err error)
}

var _ tenant.Directory = (*Orgs)(nil)

// Orgs returns the directory. It is empty until Refresh succeeds. onError, when set, is told about an org that
// cannot be loaded (a handle that is not allowed, say) and which the directory then leaves out.
func (d *DB) Orgs(onError func(handle string, err error)) *Orgs {
	o := &Orgs{db: d, onError: onError}
	o.snap.Store(tenant.NewMemory())
	return o
}

// Lookup implements tenant.Directory.
func (o *Orgs) Lookup(handle string) (*tenant.Org, bool) { return o.snap.Load().Lookup(handle) }

// IDs returns the ids of all the orgs in the snapshot.
func (o *Orgs) IDs() []tenant.ID { return o.snap.Load().IDs() }

// ByID returns the org with the id.
func (o *Orgs) ByID(id tenant.ID) (*tenant.Org, bool) { return o.snap.Load().ByID(id) }

// Refresh reads all orgs and aliases and swaps in a new snapshot. If the read fails the old snapshot stays.
func (o *Orgs) Refresh(ctx context.Context) error {
	rows, err := o.db.pool.Query(ctx, `SELECT id, handle, status, allowlist, allowlist_admin,
		requests_per_second, burst, monthly_request_cap, grace FROM engine.orgs ORDER BY id`)
	if err != nil {
		return fmt.Errorf("postgres orgs: %w", err)
	}
	defer rows.Close()
	next := tenant.NewMemory()
	var deleted []tenant.ID
	for rows.Next() {
		var (
			org    tenant.Org
			id     string
			status string
			allow  []netip.Prefix
			burst  int32
		)
		if err = rows.Scan(&id, &org.Handle, &status, &allow, &org.AllowlistAdmin,
			&org.Limits.RequestsPerSecond, &burst, &org.Limits.MonthlyRequestCap, &org.Limits.Grace); err != nil {
			return fmt.Errorf("postgres orgs: %w", err)
		}
		org.ID, org.Allowlist, org.Limits.Burst, org.Status = tenant.ID(id), allow, int(burst), tenant.Status(status)
		if org.Status == tenant.StatusDeleted {
			// Aliases cannot be added to a deleted org, so it is put as active and marked deleted afterwards.
			deleted = append(deleted, org.ID)
			org.Status = tenant.StatusActive
		}
		if err = next.Put(org); err != nil {
			o.report(org.Handle, err)
			continue
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("postgres orgs: %w", err)
	}
	if aliasErr := o.addAliases(ctx, next); aliasErr != nil {
		return aliasErr
	}
	for _, id := range deleted {
		if err = next.MarkDeleted(id); err != nil {
			o.report(string(id), err)
		}
	}
	o.snap.Store(next)
	return nil
}

func (o *Orgs) addAliases(ctx context.Context, next *tenant.Memory) error {
	rows, err := o.db.pool.Query(ctx, "SELECT alias, org_id FROM engine.org_aliases ORDER BY alias")
	if err != nil {
		return fmt.Errorf("postgres orgs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var alias, id string
		if err = rows.Scan(&alias, &id); err != nil {
			return fmt.Errorf("postgres orgs: %w", err)
		}
		if err = next.AddAlias(tenant.ID(id), alias); err != nil {
			o.report(alias, err)
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("postgres orgs: %w", err)
	}
	return nil
}

func (o *Orgs) report(handle string, err error) {
	if o.onError != nil {
		o.onError(handle, err)
	}
}

// Package tenant identifies the org a request belongs to, and resolves org handles and IP
// allowlists (DESIGN.md §4.1, §8.1).
package tenant

import (
	"context"
	"net/netip"
)

// ID identifies an org.
type ID string

// Local is the single org of standalone mode.
const Local ID = "local"

type ctxKey struct{}

// With returns a context carrying the tenant.
func With(ctx context.Context, id ID) context.Context { return context.WithValue(ctx, ctxKey{}, id) }

// From returns the tenant in ctx, or Local.
func From(ctx context.Context) ID {
	if id, ok := ctx.Value(ctxKey{}).(ID); ok {
		return id
	}
	return Local
}

// Org is one org as the engine sees it.
type Org struct {
	ID     ID
	Handle string
	// Allowlist limits mock traffic to these networks. Empty allows every client.
	Allowlist []netip.Prefix
	// AllowlistAdmin applies the allowlist to the admin API too.
	AllowlistAdmin bool
	// Status and Limits come from the org's plan (managed mode). The zero value serves.
	Status Status
	Limits Limits
	// Aliases are older handles that still resolve to the org, for good.
	Aliases []string
}

// Allows reports whether ip may send traffic to the org.
func (o *Org) Allows(ip netip.Addr) bool {
	if len(o.Allowlist) == 0 {
		return true
	}
	ip = ip.Unmap()
	for _, p := range o.Allowlist {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Directory resolves org handles (and aliases) to orgs.
type Directory interface {
	Lookup(handle string) (*Org, bool)
}

// Static is a fixed directory, used in standalone mode with the single org "local".
type Static struct {
	orgs map[string]*Org
}

var _ Directory = (*Static)(nil)

// NewStatic returns a directory holding the given orgs, keyed by handle.
func NewStatic(orgs ...*Org) *Static {
	m := make(map[string]*Org, len(orgs))
	for _, o := range orgs {
		m[o.Handle] = o
	}
	return &Static{orgs: m}
}

// Lookup returns the org with the handle.
func (s *Static) Lookup(handle string) (*Org, bool) {
	o, ok := s.orgs[handle]
	return o, ok
}

// Status is where an org stands with the platform (FR-11.5).
type Status string

// Statuses. Only Deleted is final.
const (
	StatusTrialing  Status = "trialing"
	StatusActive    Status = "active"
	StatusPastDue   Status = "past_due"
	StatusSuspended Status = "suspended"
	StatusCancelled Status = "cancelled"
	StatusDeleted   Status = "deleted"
)

// Serves reports whether an org in this status has its traffic served. A past-due org still is.
func (s Status) Serves() bool {
	switch s {
	case "", StatusTrialing, StatusActive, StatusPastDue:
		return true
	case StatusSuspended, StatusCancelled, StatusDeleted:
		return false
	}
	return false
}

// Limits are the parts of an org's plan the engine enforces itself (FR-11.5, FR-11.6).
type Limits struct {
	// RequestsPerSecond and Burst limit the org's mock traffic. Zero means the server's own setting.
	RequestsPerSecond float64
	Burst             int
	// MonthlyRequestCap is the hard cap on served requests in a calendar month, with Grace requests
	// more before traffic is refused. Zero means no cap.
	MonthlyRequestCap int64
	Grace             int64
}

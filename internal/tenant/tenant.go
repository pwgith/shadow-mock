// Package tenant identifies the org a request belongs to, and resolves org handles and IP
// allowlists (DESIGN.md §4.1, §8.1).
package tenant

import (
	"context"
	"net/netip"
	"time"
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
	// Settings are the org's own choices within its plan.
	Settings Settings
	// Aliases are older handles that still resolve to the org, for good.
	Aliases []string
}

// Allows reports whether ip may send traffic to the org.
func (o *Org) Allows(ip netip.Addr) bool {
	if len(o.Allowlist) == 0 || !o.Limits.Includes(FeatureIPAllowlist) {
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
	// MaxSessions is how many shadow mocks the org may have active at once, and IncludedSessions how many of them
	// its plan includes: a shadow mock made beyond that is allowed, with a warning in the answer, up to MaxSessions.
	// Zero means the server's own setting, and no warning.
	MaxSessions      int
	IncludedSessions int
	// MaxMappingsPerSession and MaxMappings limit the mappings in a shadow mock and in the org's global layer.
	// Zero means the server's own setting.
	MaxMappingsPerSession int
	MaxMappings           int
	// MaxURLTTL is the longest a URL-based shadow mock may live on this plan. Zero means the server's own maximum.
	MaxURLTTL time.Duration
	// Without is the set of features the plan does not include. A feature that is off keeps its settings and does not
	// apply, and applies again when the plan has it again.
	Without Feature
}

// Feature is something a plan may include. Features combine into a set.
type Feature uint8

// Features a plan can lack.
const (
	// FeatureIPAllowlist is the org's IP allowlist (FR-9.6b).
	FeatureIPAllowlist Feature = 1 << iota
)

// featureOf is the feature a name in the database stands for.
func featureOf(name string) (Feature, bool) {
	if name == "ip_allowlist" {
		return FeatureIPAllowlist, true
	}
	return 0, false
}

// ParseFeatures reads feature names, as the database holds them, into a set. A name it does not know is skipped, and
// the first of them is returned, so a newer management app cannot make an older engine forget the features it does know.
func ParseFeatures(names []string) (set Feature, unknown string) {
	for _, n := range names {
		f, ok := featureOf(n)
		if !ok {
			if unknown == "" {
				unknown = n
			}
			continue
		}
		set |= f
	}
	return set, unknown
}

// Includes reports whether the plan has the feature. A plan lacks only what it lists.
func (l Limits) Includes(f Feature) bool { return l.Without&f == 0 }

// Journal capture settings (FR-11.11d).
const (
	CaptureAll               = "all"
	CaptureSessionsUnmatched = "sessions-and-unmatched"
	CaptureOff               = "off"
)

// Settings are the choices an org makes within its plan (FR-11.11d). The zero value is the default: everything is
// journalled, with bodies, and responses are compressed.
type Settings struct {
	// JournalCapture is CaptureAll (or empty), CaptureSessionsUnmatched or CaptureOff.
	JournalCapture string
	// NoJournalBodies keeps request and response bodies out of the journal.
	NoJournalBodies bool
	// NoCompression turns off gzip of responses, whatever the client accepts.
	NoCompression bool
}

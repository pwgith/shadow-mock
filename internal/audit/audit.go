// Package audit keeps the trail of state-changing admin calls: who did what, when, with which
// credential (FR-11.15). A person's identity can be replaced by an anonymous marker, because the
// record itself has to stay.
package audit

import (
	"context"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Anonymous is what replaces a person's identity.
const Anonymous = "anonymous"

// maxEntries bounds the trail held in memory; the oldest entries go first.
const maxEntries = 100_000

// Entry is one audited call.
type Entry struct {
	At      time.Time `json:"at"`
	Org     tenant.ID `json:"org"`
	Issuer  string    `json:"issuer,omitempty"`
	Subject string    `json:"subject"`
	Key     string    `json:"key,omitempty"` // the label of a Tool app key
	TokenID string    `json:"tokenId,omitempty"`
	Method  string    `json:"method"`
	Route   string    `json:"route"`
	Status  int       `json:"status"`
}

// Store is where audit entries are kept: Trail in memory, or a database that outlives the instance.
type Store interface {
	// Record adds an entry.
	Record(ctx context.Context, e Entry)
	// List returns the org's entries, newest first.
	List(ctx context.Context, org tenant.ID) []Entry
	// Anonymise replaces a person's identity in the org's entries and returns how many it changed.
	Anonymise(ctx context.Context, org tenant.ID, person string) int
}

// Trail is the audit trail in memory.
type Trail struct {
	mu      sync.Mutex
	entries []Entry
}

var _ Store = (*Trail)(nil)

// New returns an empty trail.
func New() *Trail { return &Trail{} }

// Record adds an entry.
func (t *Trail) Record(_ context.Context, e Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, e)
	if len(t.entries) > maxEntries {
		t.entries = t.entries[len(t.entries)-maxEntries:]
	}
}

// List returns the org's entries, newest first.
func (t *Trail) List(_ context.Context, org tenant.ID) []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []Entry{}
	for i := len(t.entries) - 1; i >= 0; i-- {
		if t.entries[i].Org == org {
			out = append(out, t.entries[i])
		}
	}
	return out
}

// Anonymise replaces a person's identity in the org's entries and returns how many it changed.
func (t *Trail) Anonymise(_ context.Context, org tenant.ID, person string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for i := range t.entries {
		if e := &t.entries[i]; e.Org == org && e.Subject == person {
			e.Subject = Anonymous
			n++
		}
	}
	return n
}

package tenant

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
)

// Errors a directory write can give.
var (
	ErrHandleInvalid  = errors.New("handle_invalid")
	ErrHandleReserved = errors.New("handle_reserved")
	ErrHandleTaken    = errors.New("handle_taken")
	ErrUnknownOrg     = errors.New("unknown_org")
	ErrDeleted        = errors.New("org_deleted")
)

// handlePattern is the shape of a handle: 3 to 40 characters of a-z, 0-9 and hyphen, starting with a
// letter and not ending with a hyphen (FR-9.6). It is DNS-label safe, and never starts with "_".
var handlePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)

// reservedHandles can never be a handle, because they name something of the platform or the engine.
func reservedHandles() []string {
	return []string{"www", "api", "admin", "app", "mail", "static", "root", "status", "support", "console", "docs", "health", "metrics"}
}

// ValidateHandle checks a handle against the rules, not against other orgs.
func ValidateHandle(h string) error {
	if !handlePattern.MatchString(h) {
		return fmt.Errorf("%w: a handle is 3 to 40 characters of a-z, 0-9 and -, starts with a letter and does not end with a hyphen", ErrHandleInvalid)
	}
	if slices.Contains(reservedHandles(), h) {
		return fmt.Errorf("%w: %q is reserved", ErrHandleReserved, h)
	}
	return nil
}

// Memory is a directory of many orgs that can change while the engine runs. It is the managed-mode
// directory; a database-backed one replaces it without changing what asks for orgs.
type Memory struct {
	mu       sync.RWMutex
	byHandle map[string]*Org // handles and aliases
	byID     map[ID]*Org
}

var _ Directory = (*Memory)(nil)

// NewMemory returns an empty directory.
func NewMemory() *Memory {
	return &Memory{byHandle: map[string]*Org{}, byID: map[ID]*Org{}}
}

// Lookup resolves a handle or an alias. The org is a snapshot: a later change makes a new Org.
func (m *Memory) Lookup(handle string) (*Org, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.byHandle[handle]
	return o, ok
}

// IDs returns the ids of all the orgs, deleted ones too, in order.
func (m *Memory) IDs() []ID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ID, 0, len(m.byID))
	for id := range m.byID {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// ByID returns the org with the id.
func (m *Memory) ByID(id ID) (*Org, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.byID[id]
	return o, ok
}

// Put adds an org, or updates the one with the same id. A deleted org is a tombstone that nothing
// changes or recreates (TA-14). A handle that belongs to another org, or to a deleted one, is refused.
func (m *Memory) Put(o Org) error {
	if err := ValidateHandle(o.Handle); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.byID[o.ID]; ok && cur.Status == StatusDeleted {
		return fmt.Errorf("%w: org %s", ErrDeleted, o.ID)
	}
	if owner, taken := m.byHandle[o.Handle]; taken && owner.ID != o.ID {
		return takenError(owner, o.Handle)
	}
	next := o
	if cur, ok := m.byID[o.ID]; ok {
		next.Aliases = slices.Clone(cur.Aliases)
		if cur.Handle != o.Handle && !slices.Contains(next.Aliases, cur.Handle) {
			next.Aliases = append(next.Aliases, cur.Handle) // a renamed handle stays as a permanent alias
		}
	}
	m.byID[o.ID] = &next
	m.byHandle[o.Handle] = &next
	for _, a := range next.Aliases {
		m.byHandle[a] = &next
	}
	return nil
}

// AddAlias gives an org another handle that resolves to it for good.
func (m *Memory) AddAlias(id ID, alias string) error {
	if err := ValidateHandle(alias); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownOrg, id)
	}
	if cur.Status == StatusDeleted {
		return fmt.Errorf("%w: org %s", ErrDeleted, id)
	}
	if owner, taken := m.byHandle[alias]; taken && owner.ID != id {
		return takenError(owner, alias)
	}
	next := *cur
	next.Aliases = append(slices.Clone(cur.Aliases), alias)
	m.byID[id] = &next
	m.byHandle[next.Handle] = &next
	for _, a := range next.Aliases {
		m.byHandle[a] = &next
	}
	return nil
}

// Update changes an org by id and publishes the result as a new snapshot. A deleted org is not changed.
func (m *Memory) Update(id ID, change func(*Org)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownOrg, id)
	}
	if cur.Status == StatusDeleted {
		return fmt.Errorf("%w: org %s", ErrDeleted, id)
	}
	next := *cur
	next.Aliases = slices.Clone(cur.Aliases)
	change(&next)
	next.ID, next.Handle = cur.ID, cur.Handle // the id and the handle change only through Put
	m.byID[id] = &next
	m.byHandle[next.Handle] = &next
	for _, a := range next.Aliases {
		m.byHandle[a] = &next
	}
	return nil
}

// SetStatus changes an org's status. Deleted is final.
func (m *Memory) SetStatus(id ID, s Status) error {
	return m.Update(id, func(o *Org) { o.Status = s })
}

// MarkDeleted makes an org a tombstone: it is refused from then on, and its handle and aliases stay
// reserved. Doing it again is not an error.
func (m *Memory) MarkDeleted(id ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownOrg, id)
	}
	next := *cur
	next.Aliases = slices.Clone(cur.Aliases)
	next.Status = StatusDeleted
	m.byID[id] = &next
	m.byHandle[next.Handle] = &next
	for _, a := range next.Aliases {
		m.byHandle[a] = &next
	}
	return nil
}

func takenError(owner *Org, handle string) error {
	if owner.Status == StatusDeleted {
		return fmt.Errorf("%w: %q belonged to a deleted org", ErrHandleReserved, handle)
	}
	return fmt.Errorf("%w: %q belongs to another org", ErrHandleTaken, handle)
}

// Package journal records every mock request with what served it (FR-5.1, FR-5.4). Standalone
// keeps a bounded in-memory ring; SaaS adds a Redis hot tier and a warm tier (DESIGN.md §5.2).
package journal

import (
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Detection records how a key-based shadow mock was found in the request.
type Detection struct {
	Stage         string
	Offset        int
	ScanTruncated bool
	Ambiguous     []string
}

// Response is the response that was sent.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Entry is one served request.
type Entry struct {
	ID       string
	LoggedAt time.Time
	Tenant   tenant.ID
	// SessionID is the most specific session the request was attributed to ("" = global).
	SessionID string
	// Sessions lists every session in the request's stack, most specific first. A session's
	// journal holds the requests whose stack includes it (FR-7.11).
	Sessions []string
	// Layer is the kind of layer that served the request: key-based, url-based or global;
	// "" when nothing matched.
	Layer       string
	Request     *match.Request
	AbsoluteURL string
	// Stub is the mapping that served the request, or nil.
	Stub       *model.StubMapping
	StubScope  store.Scope
	Definition *model.ResponseDefinition
	Response   Response
	Detection  *Detection
	CallNumber int64
	// ProcessTime and AddedDelay are in milliseconds.
	ProcessTime int64
	AddedDelay  int64
}

// Matched reports whether a stub served the request.
func (e *Entry) Matched() bool { return e.Stub != nil }

// InScope reports whether the entry belongs to the journal of scope s.
func (e *Entry) InScope(s store.Scope) bool {
	if e.Tenant != s.Tenant {
		return false
	}
	return s.IsGlobal() || slices.Contains(e.Sessions, s.SessionID)
}

// Memory is a bounded in-memory journal, most recent entries kept.
type Memory struct {
	mu      sync.RWMutex
	max     int
	entries []*Entry // oldest first
}

// NewMemory returns a journal holding at most maxEntries entries; 0 disables it.
func NewMemory(maxEntries int) *Memory { return &Memory{max: maxEntries} }

// Enabled reports whether the journal records anything.
func (j *Memory) Enabled() bool { return j.max > 0 }

// Append records an entry, dropping the oldest when full.
func (j *Memory) Append(e *Entry) {
	if j.max <= 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, e)
	if over := len(j.entries) - j.max; over > 0 {
		j.entries = slices.Delete(j.entries, 0, over)
	}
}

// Query returns entries in scope s that satisfy keep, most recent first. limit 0 means all.
func (j *Memory) Query(s store.Scope, keep func(*Entry) bool, limit int) []*Entry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	var out []*Entry
	for i := len(j.entries) - 1; i >= 0; i-- {
		e := j.entries[i]
		if !e.InScope(s) || (keep != nil && !keep(e)) {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// Get returns one entry in scope.
func (j *Memory) Get(s store.Scope, id string) (*Entry, bool) {
	found := j.Query(s, func(e *Entry) bool { return e.ID == id }, 1)
	if len(found) == 0 {
		return nil, false
	}
	return found[0], true
}

// Remove deletes the entries in scope s that satisfy drop, and returns them.
func (j *Memory) Remove(s store.Scope, drop func(*Entry) bool) []*Entry {
	j.mu.Lock()
	defer j.mu.Unlock()
	var removed []*Entry
	j.entries = slices.DeleteFunc(j.entries, func(e *Entry) bool {
		if e.InScope(s) && (drop == nil || drop(e)) {
			removed = append(removed, e)
			return true
		}
		return false
	})
	return removed
}

// Count returns how many entries in scope s satisfy keep.
func (j *Memory) Count(s store.Scope, keep func(*Entry) bool) int {
	return len(j.Query(s, keep, 0))
}

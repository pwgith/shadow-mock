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
	Stub *model.StubMapping
	// PassThrough marks a request the browser proxy sent on to its origin because no mapping matched;
	// WireMock counts that as matched too.
	PassThrough bool
	StubScope   store.Scope
	Definition  *model.ResponseDefinition
	Response    Response
	Detection   *Detection
	CallNumber  int64
	// ProcessTime and AddedDelay are in milliseconds.
	ProcessTime int64
	AddedDelay  int64
}

// Matched reports whether a stub served the request, or the browser proxy passed it on.
func (e *Entry) Matched() bool { return e.Stub != nil || e.PassThrough }

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
	maxBody int      // bodies longer than this are cut when an entry is kept; 0 keeps them whole
	maxSize int64    // the most memory the entries may hold in all; 0 means no budget
	size    int64    // memory the entries hold now, as sizeOf counts it
	entries []*Entry // oldest first
}

// NewMemory returns a journal holding at most maxEntries entries; 0 disables it.
func NewMemory(maxEntries int) *Memory { return &Memory{max: maxEntries} }

// LimitBodies makes the journal keep at most n bytes of each request and response body. Without
// a limit, a journal of N entries can hold N times the largest body the server accepts.
func (j *Memory) LimitBodies(n int) { j.maxBody = n }

// LimitSize gives the journal a memory budget. Entries keep their bodies whole, and the oldest
// are dropped when the entries together outgrow the budget, so memory stays bounded without
// cutting anything a test may want to read back. The newest entry is always kept.
func (j *Memory) LimitSize(bytes int64) { j.maxSize = bytes }

// Enabled reports whether the journal records anything.
func (j *Memory) Enabled() bool { return j.max > 0 }

// Append records an entry, dropping the oldest when full.
func (j *Memory) Append(e *Entry) {
	if j.max <= 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	e = j.trimmed(e)
	j.entries = append(j.entries, e)
	j.size += sizeOf(e)
	over := max(len(j.entries)-j.max, 0)
	dropped := j.droppedSize(over)
	for j.maxSize > 0 && over < len(j.entries)-1 && j.size-dropped > j.maxSize {
		dropped += sizeOf(j.entries[over])
		over++
	}
	if over > 0 {
		j.size -= dropped
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
			j.size -= sizeOf(e)
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

// trimmed returns e, or a copy of it whose bodies are cut to the journal's body limit.
func (j *Memory) trimmed(e *Entry) *Entry {
	if j.maxBody <= 0 {
		return e
	}
	reqBig := e.Request != nil && len(e.Request.Body) > j.maxBody
	respBig := len(e.Response.Body) > j.maxBody
	if !reqBig && !respBig {
		return e
	}
	cp := *e
	if reqBig {
		cp.Request = e.Request.WithBody(slices.Clone(e.Request.Body[:j.maxBody]))
	}
	if respBig {
		cp.Response.Body = slices.Clone(e.Response.Body[:j.maxBody])
	}
	return &cp
}

// droppedSize is the memory held by the n oldest entries.
func (j *Memory) droppedSize(n int) int64 {
	var total int64
	for _, e := range j.entries[:n] {
		total += sizeOf(e)
	}
	return total
}

// entryOverhead stands in for everything in an entry besides its bodies and headers: the stub
// reference, the detection record, the strings and the structs themselves.
const entryOverhead = 2048

// sizeOf estimates the memory an entry holds. It need not be exact; it keeps the budget honest.
func sizeOf(e *Entry) int64 {
	n := int64(entryOverhead) + int64(len(e.Response.Body)) + headerSize(e.Response.Header)
	if e.Request != nil {
		n += int64(len(e.Request.Body)) + int64(len(e.Request.RawQuery)) + headerSize(e.Request.Header)
	}
	return n
}

func headerSize(h http.Header) int64 {
	var n int64
	for k, vs := range h {
		n += int64(len(k))
		for _, v := range vs {
			n += int64(len(v))
		}
	}
	return n
}

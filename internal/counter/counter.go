// Package counter keeps call counters for call-count responses (DESIGN.md §6.3, FR-4.1–FR-4.3).
// A counter belongs to a request scope (who is calling) and a stub (which layer's stub served),
// so a session's calls never change the numbers another session or global traffic sees (FR-7.6).
package counter

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pwgith/shadow-mock/internal/store"
)

// Key identifies one counter.
type Key struct {
	// RequestScope is the most specific layer of the request's stack: the session the request
	// was attributed to, or global.
	RequestScope store.Scope
	// StubScope is the layer the serving stub belongs to.
	StubScope store.Scope
	StubID    string
}

// Entry is a counter's state.
type Entry struct {
	Key          Key
	Count        int64
	LastCalledAt time.Time
}

// Counters stores call counters. Next is the only per-request operation, and is atomic.
type Counters interface {
	// Next increments the counter and returns the new call number (1, 2, 3, …).
	Next(ctx context.Context, k Key, now time.Time) (int64, error)
	// Peek returns the call number the next call would get, without incrementing.
	Peek(ctx context.Context, k Key) (int64, error)
	// List returns the counters whose request scope is s.
	List(ctx context.Context, s store.Scope) ([]Entry, error)
	// Set sets how many calls have happened. The next call gets n + 1.
	Set(ctx context.Context, k Key, n int64) error
	// Reset zeroes the counters in request scope s for stubID, or all of them if stubID is "".
	Reset(ctx context.Context, s store.Scope, stubID string) error
	// ForgetStub drops every counter of a stub, in every request scope (the stub was replaced
	// or deleted, FR-4.2).
	ForgetStub(ctx context.Context, stubScope store.Scope, stubID string) error
	// ForgetScope drops every counter whose request or stub scope is s (session deleted).
	ForgetScope(ctx context.Context, s store.Scope) error
}

// Memory is the standalone implementation: one atomic counter per key.
type Memory struct {
	mu      sync.RWMutex
	entries map[Key]*memEntry
}

type memEntry struct {
	count atomic.Int64
	last  atomic.Int64 // unix nanoseconds
}

var _ Counters = (*Memory)(nil)

// NewMemory returns an empty in-memory counter store.
func NewMemory() *Memory { return &Memory{entries: map[Key]*memEntry{}} }

func (m *Memory) entry(k Key) *memEntry {
	m.mu.RLock()
	e := m.entries[k]
	m.mu.RUnlock()
	if e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e = m.entries[k]; e == nil {
		e = &memEntry{}
		m.entries[k] = e
	}
	return e
}

// Next implements Counters.
func (m *Memory) Next(_ context.Context, k Key, now time.Time) (int64, error) {
	e := m.entry(k)
	n := e.count.Add(1)
	e.last.Store(now.UnixNano())
	return n, nil
}

// Peek implements Counters.
func (m *Memory) Peek(_ context.Context, k Key) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e := m.entries[k]; e != nil {
		return e.count.Load() + 1, nil
	}
	return 1, nil
}

// List implements Counters.
func (m *Memory) List(_ context.Context, s store.Scope) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Entry
	for k, e := range m.entries {
		if k.RequestScope != s {
			continue
		}
		en := Entry{Key: k, Count: e.count.Load()}
		if last := e.last.Load(); last != 0 {
			en.LastCalledAt = time.Unix(0, last).UTC()
		}
		out = append(out, en)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.StubID != out[j].Key.StubID {
			return out[i].Key.StubID < out[j].Key.StubID
		}
		return out[i].Key.StubScope.SessionID > out[j].Key.StubScope.SessionID
	})
	return out, nil
}

// Set implements Counters.
func (m *Memory) Set(_ context.Context, k Key, n int64) error {
	m.entry(k).count.Store(n)
	return nil
}

// Reset implements Counters.
func (m *Memory) Reset(_ context.Context, s store.Scope, stubID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.entries {
		if k.RequestScope == s && (stubID == "" || k.StubID == stubID) {
			delete(m.entries, k)
		}
	}
	return nil
}

// ForgetStub implements Counters.
func (m *Memory) ForgetStub(_ context.Context, stubScope store.Scope, stubID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.entries {
		if k.StubScope == stubScope && k.StubID == stubID {
			delete(m.entries, k)
		}
	}
	return nil
}

// ForgetScope implements Counters.
func (m *Memory) ForgetScope(_ context.Context, s store.Scope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.entries {
		if k.RequestScope == s || k.StubScope == s {
			delete(m.entries, k)
		}
	}
	return nil
}

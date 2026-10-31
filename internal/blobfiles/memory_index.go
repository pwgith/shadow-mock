package blobfiles

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// MemoryIndex is an Index in memory, for tests and single-instance runs.
type MemoryIndex struct {
	mu    sync.RWMutex
	files map[tenant.ID]map[string]Entry
}

var _ Index = (*MemoryIndex)(nil)

// NewMemoryIndex returns an empty index.
func NewMemoryIndex() *MemoryIndex { return &MemoryIndex{files: map[tenant.ID]map[string]Entry{}} }

// Lookup implements Index.
func (m *MemoryIndex) Lookup(_ context.Context, org tenant.ID, name string) (Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.files[org][name]; ok {
		return e, nil
	}
	return Entry{}, store.ErrNotFound
}

// Put implements Index.
func (m *MemoryIndex) Put(_ context.Context, org tenant.ID, name string, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.files[org] == nil {
		m.files[org] = map[string]Entry{}
	}
	m.files[org][name] = e
	return nil
}

// Delete implements Index.
func (m *MemoryIndex) Delete(_ context.Context, org tenant.ID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[org][name]; !ok {
		return fmt.Errorf("%s: %w", name, store.ErrNotFound)
	}
	delete(m.files[org], name)
	return nil
}

// DeleteAll implements Index.
func (m *MemoryIndex) DeleteAll(_ context.Context, org tenant.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, org)
	return nil
}

// List implements Index.
func (m *MemoryIndex) List(_ context.Context, org tenant.ID) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.files[org]))
	for n := range m.files[org] {
		out = append(out, n)
	}
	slices.Sort(out)
	return out, nil
}

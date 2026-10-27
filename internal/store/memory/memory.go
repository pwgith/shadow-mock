// Package memory holds mappings and files in memory: the default for sessions and tests.
package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
)

// Mappings is an in-memory MappingRepository.
type Mappings struct {
	mu     sync.RWMutex
	scopes map[store.Scope]*scopeMappings
}

type scopeMappings struct {
	order []string
	byID  map[string]model.StubMapping
}

var _ store.MappingRepository = (*Mappings)(nil)

// NewMappings returns an empty repository.
func NewMappings() *Mappings { return &Mappings{scopes: map[store.Scope]*scopeMappings{}} }

// List implements store.MappingRepository.
func (r *Mappings) List(_ context.Context, s store.Scope) ([]model.StubMapping, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sm := r.scopes[s]
	if sm == nil {
		return nil, nil
	}
	out := make([]model.StubMapping, 0, len(sm.order))
	for _, id := range sm.order {
		out = append(out, sm.byID[id])
	}
	return out, nil
}

// Get implements store.MappingRepository.
func (r *Mappings) Get(_ context.Context, s store.Scope, id string) (model.StubMapping, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sm := r.scopes[s]; sm != nil {
		if m, ok := sm.byID[id]; ok {
			return m, nil
		}
	}
	return model.StubMapping{}, fmt.Errorf("mapping %s in %s: %w", id, s, store.ErrNotFound)
}

// Put implements store.MappingRepository.
func (r *Mappings) Put(_ context.Context, s store.Scope, m model.StubMapping) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sm := r.scopes[s]
	if sm == nil {
		sm = &scopeMappings{byID: map[string]model.StubMapping{}}
		r.scopes[s] = sm
	}
	if _, exists := sm.byID[m.ID]; !exists {
		sm.order = append(sm.order, m.ID)
	}
	sm.byID[m.ID] = m
	return nil
}

// Delete implements store.MappingRepository.
func (r *Mappings) Delete(_ context.Context, s store.Scope, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sm := r.scopes[s]
	if sm == nil {
		return fmt.Errorf("mapping %s in %s: %w", id, s, store.ErrNotFound)
	}
	if _, ok := sm.byID[id]; !ok {
		return fmt.Errorf("mapping %s in %s: %w", id, s, store.ErrNotFound)
	}
	delete(sm.byID, id)
	sm.order = slices.DeleteFunc(sm.order, func(x string) bool { return x == id })
	return nil
}

// DeleteAll implements store.MappingRepository.
func (r *Mappings) DeleteAll(_ context.Context, s store.Scope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.scopes, s)
	return nil
}

// Files is an in-memory FileRepository.
type Files struct {
	mu     sync.RWMutex
	scopes map[store.Scope]map[string][]byte
}

var _ store.FileRepository = (*Files)(nil)

// NewFiles returns an empty repository.
func NewFiles() *Files { return &Files{scopes: map[store.Scope]map[string][]byte{}} }

// Read implements store.FileRepository.
func (r *Files) Read(_ context.Context, s store.Scope, name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if b, ok := r.scopes[s][clean(name)]; ok {
		return slices.Clone(b), nil
	}
	return nil, fmt.Errorf("file %s in %s: %w", name, s, store.ErrNotFound)
}

// Put implements store.FileRepository.
func (r *Files) Put(_ context.Context, s store.Scope, name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scopes[s] == nil {
		r.scopes[s] = map[string][]byte{}
	}
	r.scopes[s][clean(name)] = slices.Clone(data)
	return nil
}

// Delete implements store.FileRepository.
func (r *Files) Delete(_ context.Context, s store.Scope, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.scopes[s][clean(name)]; !ok {
		return fmt.Errorf("file %s in %s: %w", name, s, store.ErrNotFound)
	}
	delete(r.scopes[s], clean(name))
	return nil
}

// DeleteAll implements store.FileRepository.
func (r *Files) DeleteAll(_ context.Context, s store.Scope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.scopes, s)
	return nil
}

// List implements store.FileRepository.
func (r *Files) List(_ context.Context, s store.Scope) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.scopes[s]))
	for name := range r.scopes[s] {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func clean(name string) string { return strings.TrimPrefix(name, "/") }

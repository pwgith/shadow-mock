package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
)

// Memory is a Store in memory, for tests and for single-process runs.
type Memory struct {
	mu      sync.RWMutex
	objects map[string]memObject
}

type memObject struct {
	data        []byte
	contentType string
}

var _ Store = (*Memory)(nil)

// NewMemory returns an empty store.
func NewMemory() *Memory { return &Memory{objects: map[string]memObject{}} }

// Put implements Store.
func (m *Memory) Put(_ context.Context, key string, data []byte, meta Meta) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memObject{data: slices.Clone(data), contentType: meta.ContentType}
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, key string, rng *Range) (io.ReadCloser, error) {
	if err := ValidKey(key); err != nil {
		return nil, err
	}
	m.mu.RLock()
	o, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%s: %w", key, ErrNotFound)
	}
	return io.NopCloser(bytes.NewReader(Slice(o.data, rng))), nil
}

// Stat implements Store.
func (m *Memory) Stat(_ context.Context, key string) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.objects[key]
	if !ok {
		return Info{}, fmt.Errorf("%s: %w", key, ErrNotFound)
	}
	return Info{Size: int64(len(o.data)), ContentType: o.contentType}, nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, prefix string) ([]string, error) {
	if err := ValidPrefix(prefix); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []string{}
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Slice returns the part of data in rng, as an object store does: a range that starts past the end is empty, and
// one that runs past the end stops there.
func Slice(data []byte, rng *Range) []byte {
	if rng == nil {
		return data
	}
	if rng.Off < 0 || rng.Off >= int64(len(data)) {
		return nil
	}
	end := int64(len(data))
	if rng.Len > 0 {
		end = min(end, rng.Off+rng.Len)
	}
	return data[rng.Off:end]
}

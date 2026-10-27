// Package engine resolves requests against layers of compiled stubs (DESIGN.md §6). Each
// layer's snapshot is immutable and read through an atomic pointer, so the request path takes
// no locks; writes build a new snapshot and swap it in.
package engine

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
)

// CompiledStub is a mapping with its matcher compiled once.
type CompiledStub struct {
	Mapping model.StubMapping
	Matcher *match.RequestMatcher
	Scope   store.Scope
	Kind    session.Kind
	order   int // insertion index; later wins ties
}

// ID returns the mapping id.
func (cs *CompiledStub) ID() string { return cs.Mapping.ID }

// before orders stubs by priority (lower first), then most recently added first (FR-2.6).
func before(a, b *CompiledStub) int {
	if c := cmp.Compare(a.Mapping.Priority, b.Mapping.Priority); c != 0 {
		return c
	}
	return cmp.Compare(b.order, a.order)
}

type compiledMask struct {
	id      string
	matcher *match.RequestMatcher
}

// Snapshot is an immutable, compiled view of one layer's mappings and masks.
type Snapshot struct {
	Scope store.Scope
	Kind  session.Kind
	stubs []*CompiledStub // sorted by before
	byID  map[string]*CompiledStub
	// byPath buckets stubs that need an exact path; wildcard holds the rest. Most requests
	// then evaluate only a few candidates (DESIGN.md §6.1).
	byPath   map[string][]*CompiledStub
	wildcard []*CompiledStub
	masks    []compiledMask
}

// CompileMapping compiles a mapping's request pattern, for validation and snapshots.
func CompileMapping(m *model.StubMapping, o match.Options) (*match.RequestMatcher, error) {
	rm, err := match.Compile(&m.Request, o)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	return rm, nil
}

// BuildSnapshot compiles mappings (in insertion order) and masks. Mappings that fail to
// compile are left out and reported.
func BuildSnapshot(scope store.Scope, kind session.Kind, mappings []model.StubMapping, masks []model.Mask, o match.Options) (*Snapshot, []error) {
	s := &Snapshot{Scope: scope, Kind: kind, byID: make(map[string]*CompiledStub, len(mappings)), byPath: map[string][]*CompiledStub{}}
	var errs []error
	for i := range mappings {
		rm, err := CompileMapping(&mappings[i], o)
		if err != nil {
			errs = append(errs, fmt.Errorf("mapping %s: %w", mappings[i].ID, err))
			continue
		}
		cs := &CompiledStub{Mapping: mappings[i], Matcher: rm, Scope: scope, Kind: kind, order: i}
		s.stubs = append(s.stubs, cs)
		s.byID[cs.ID()] = cs
	}
	slices.SortStableFunc(s.stubs, before)
	for _, cs := range s.stubs {
		if p, ok := cs.Matcher.LiteralPath(); ok {
			s.byPath[p] = append(s.byPath[p], cs)
		} else {
			s.wildcard = append(s.wildcard, cs)
		}
	}
	for i := range masks {
		cm := compiledMask{id: masks[i].ID}
		if masks[i].Request != nil {
			rm, err := match.Compile(masks[i].Request, o)
			if err != nil {
				errs = append(errs, fmt.Errorf("mask %d: %w", i, err))
				continue
			}
			cm.matcher = rm
		}
		s.masks = append(s.masks, cm)
	}
	return s, errs
}

// Stubs returns the stubs in evaluation order.
func (s *Snapshot) Stubs() []*CompiledStub { return s.stubs }

// Stub returns a stub by id.
func (s *Snapshot) Stub(id string) (*CompiledStub, bool) {
	cs, ok := s.byID[id]
	return cs, ok
}

// candidates calls fn for each stub that could match r, in evaluation order, until fn
// returns false. It merges the request path's bucket with the wildcard stubs.
func (s *Snapshot) candidates(r *match.Request, fn func(*CompiledStub) bool) {
	bucket := s.byPath[r.Path]
	i, j := 0, 0
	for i < len(bucket) || j < len(s.wildcard) {
		var next *CompiledStub
		switch {
		case j >= len(s.wildcard) || (i < len(bucket) && before(bucket[i], s.wildcard[j]) <= 0):
			next = bucket[i]
			i++
		default:
			next = s.wildcard[j]
			j++
		}
		if m := next.Matcher.Method(); m != "" && !strings.EqualFold(m, r.Method) {
			continue
		}
		if !fn(next) {
			return
		}
	}
}

// Layer is one layer of the stack. Its snapshot is swapped atomically.
type Layer struct {
	Scope store.Scope
	Kind  session.Kind
	snap  atomic.Pointer[Snapshot]
}

// Snapshot returns the current snapshot.
func (l *Layer) Snapshot() *Snapshot { return l.snap.Load() }

// Stack is the ordered list of layers for a request: most specific first, global last.
type Stack []*Layer

// RequestScope is the scope call counters are kept under: the stack's most specific layer.
func (st Stack) RequestScope() store.Scope {
	if len(st) == 0 {
		return store.Scope{}
	}
	return st[0].Scope
}

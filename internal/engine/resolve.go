package engine

import (
	"sort"

	"github.com/pwgith/shadow-mock/internal/match"
)

// Result is the outcome of resolving a request.
type Result struct {
	Stub  *CompiledStub
	Layer *Layer
}

// NearMiss is a stub that came close to matching.
type NearMiss struct {
	Stub     *CompiledStub
	Layer    *Layer
	Distance float64
}

// hidden tracks parent stubs hidden by more specific layers: by id (overrides, FR-7.7, and id
// masks) and by pattern masks that match the request (FR-7.8). A stack has at most three
// layers, so it checks the earlier snapshots directly instead of building a set.
type hidden struct {
	above   [3]*Snapshot
	n       int
	blocked bool // a pattern mask matched this request; every parent stub is hidden
}

func (h *hidden) hides(cs *CompiledStub) bool {
	if h.blocked {
		return true
	}
	for _, s := range h.above[:h.n] {
		if _, ok := s.byID[cs.ID()]; ok {
			return true
		}
		for _, m := range s.masks {
			if m.id == cs.ID() {
				return true
			}
		}
	}
	return false
}

func (h *hidden) add(s *Snapshot, r *match.Request) {
	if h.n < len(h.above) {
		h.above[h.n] = s
		h.n++
	}
	for _, m := range s.masks {
		if m.matcher != nil && m.matcher.Match(r) {
			h.blocked = true
		}
	}
}

// Resolve finds the stub that serves r. The layer always beats priority (FR-7.5): layers are
// tried in order, and priority only orders stubs within one layer. Matching never depends on
// state, so this is a pure function of the request and the snapshots (DESIGN.md §6.2).
func Resolve(r *match.Request, stack Stack) Result {
	var h hidden
	for _, layer := range stack {
		snap := layer.Snapshot()
		if snap == nil {
			continue
		}
		var found *CompiledStub
		snap.candidates(r, func(cs *CompiledStub) bool {
			if h.hides(cs) || !cs.Matcher.Match(r) {
				return true
			}
			found = cs
			return false
		})
		if found != nil {
			return Result{Stub: found, Layer: layer}
		}
		h.add(snap, r)
	}
	return Result{}
}

// NearMisses ranks the stubs in the stack by how close they come to matching r, closest
// first, skipping stubs hidden by more specific layers (FR-5.3).
func NearMisses(r *match.Request, stack Stack, limit int) []NearMiss {
	var out []NearMiss
	var h hidden
	for _, layer := range stack {
		snap := layer.Snapshot()
		if snap == nil {
			continue
		}
		for _, cs := range snap.stubs {
			if h.hides(cs) {
				continue
			}
			res := cs.Matcher.Score(r)
			out = append(out, NearMiss{Stub: cs, Layer: layer, Distance: res.Distance})
		}
		h.add(snap, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Distance < out[j].Distance })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

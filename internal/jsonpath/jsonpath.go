// Package jsonpath evaluates JSONPath expressions the way Jayway JsonPath does, because that is
// the library WireMock uses (FR-10.1). The standard (RFC 9535) differs from Jayway in ways a
// mapping can notice: Jayway filters test an object itself when applied to one, supports the
// operators in, nin, subsetof, anyof, noneof, contains, size and empty, matches regular
// expressions against the whole value, compares a number with a string leniently, and has path
// functions such as size() and keys().
//
// The behaviour here is checked against a real WireMock by testdata/jayway_wiremock_3_13_2.json.
package jsonpath

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Path is a compiled JSONPath expression.
type Path struct {
	src      string
	tokens   []token
	function *function
	definite bool
}

// String returns the expression the path was compiled from.
func (p *Path) String() string { return p.src }

// Definite reports whether the path can name at most one value (no wildcard, deep scan, filter,
// slice or union). Jayway returns the value itself for a definite path and a list otherwise.
func (p *Path) Definite() bool { return p.definite }

// Function returns the name of the function the path ends in (sum, keys and so on), or "".
func (p *Path) Function() string {
	if p.function == nil {
		return ""
	}
	return p.function.name
}

// Parse compiles an expression. Like Jayway, an expression that does not start with $ or @ is
// read as if it started with "$.".
func Parse(expr string) (*Path, error) {
	src := strings.TrimSpace(expr)
	if src == "" {
		return nil, errors.New("jsonpath: the expression is empty")
	}
	if src[0] != '$' && src[0] != '@' {
		src = "$." + src
	}
	p := &parser{in: []rune(src)}
	path, err := p.path()
	if err != nil {
		return nil, fmt.Errorf("jsonpath %q: %w", expr, err)
	}
	if !p.done() {
		return nil, fmt.Errorf("jsonpath %q: unexpected %q at %d", expr, string(p.in[p.pos]), p.pos)
	}
	path.src = expr
	return path, nil
}

// Get evaluates the path against a decoded JSON document and returns what it found, in document
// order (object keys are visited in sorted order). A path that finds nothing returns an empty slice.
// A JSON null that the path reaches is returned as nil.
func (p *Path) Get(doc any) []any {
	vals, _ := p.eval(doc, doc)
	return vals
}

func (p *Path) eval(root, current any) ([]any, bool) {
	nodes := []any{current}
	for _, t := range p.tokens {
		nodes = t.apply(root, nodes)
		if len(nodes) == 0 {
			break
		}
	}
	if p.function != nil {
		var in any
		switch {
		case len(nodes) == 0 && !p.definite:
			in = []any{}
		case p.definite && len(nodes) > 0:
			in = nodes[0]
		case p.definite:
			return nil, false
		default:
			in = nodes
		}
		out, ok := p.function.call(root, in, p.definite)
		if !ok {
			return nil, false
		}
		return []any{out}, true
	}
	return nodes, len(nodes) > 0
}

// Matches reports whether the path finds something, as WireMock decides a bare matchesJsonPath: the
// result must not be absent, null, or an empty array or object. Zero, false and "" count as found.
func (p *Path) Matches(doc any) bool {
	for _, r := range p.Get(doc) {
		switch t := r.(type) {
		case nil:
		case []any:
			if len(t) > 0 {
				return true
			}
		case map[string]any:
			if len(t) > 0 {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// token is one step of a path.
type token interface {
	apply(root any, nodes []any) []any
}

// deepScan makes the following token apply to every node and everything below it.
type deepScan struct{ next token }

func (d deepScan) apply(root any, nodes []any) []any {
	var all []any
	for _, n := range nodes {
		collect(n, &all)
	}
	return d.next.apply(root, all)
}

func collect(n any, out *[]any) {
	switch t := n.(type) {
	case map[string]any:
		*out = append(*out, n)
		for _, k := range sortedKeys(t) {
			collect(t[k], out)
		}
	case []any:
		*out = append(*out, n)
		for _, e := range t {
			collect(e, out)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// names selects properties by name: one name is a child, several are a union.
type names []string

func (n names) apply(_ any, nodes []any) []any {
	var out []any
	for _, node := range nodes {
		m, ok := node.(map[string]any)
		if !ok {
			continue
		}
		for _, name := range n {
			if v, found := m[name]; found {
				out = append(out, v)
			}
		}
	}
	return out
}

// wildcard selects every property or element.
type wildcard struct{}

func (wildcard) apply(_ any, nodes []any) []any {
	var out []any
	for _, node := range nodes {
		switch t := node.(type) {
		case map[string]any:
			for _, k := range sortedKeys(t) {
				out = append(out, t[k])
			}
		case []any:
			out = append(out, t...)
		}
	}
	return out
}

// indexes selects array elements by position; a negative index counts from the end.
type indexes []int

func (ix indexes) apply(_ any, nodes []any) []any {
	var out []any
	for _, node := range nodes {
		arr, ok := node.([]any)
		if !ok {
			continue
		}
		for _, i := range ix {
			if i < 0 {
				i += len(arr)
			}
			if i >= 0 && i < len(arr) {
				out = append(out, arr[i])
			}
		}
	}
	return out
}

// slice selects array elements from start up to end (exclusive). A missing bound is nil.
type slice struct{ start, end *int }

func (s slice) apply(_ any, nodes []any) []any {
	var out []any
	for _, node := range nodes {
		arr, ok := node.([]any)
		if !ok {
			continue
		}
		lo, hi := 0, len(arr)
		if s.start != nil {
			lo = *s.start
			if lo < 0 {
				lo += len(arr)
			}
		}
		if s.end != nil {
			hi = *s.end
			if hi < 0 {
				hi += len(arr)
			}
		}
		lo, hi = max(lo, 0), min(hi, len(arr))
		if lo < hi {
			out = append(out, arr[lo:hi]...)
		}
	}
	return out
}

// filter keeps the elements of an array that satisfy a predicate. Applied to an object it keeps
// the object itself when the object satisfies it, which is what Jayway does.
type filter struct{ test expr }

func (f filter) apply(root any, nodes []any) []any {
	var out []any
	for _, node := range nodes {
		switch t := node.(type) {
		case []any:
			for _, e := range t {
				if f.test.truth(root, e) {
					out = append(out, e)
				}
			}
		case map[string]any:
			if f.test.truth(root, node) {
				out = append(out, node)
			}
		}
	}
	return out
}

// number returns v as a float when it is a JSON number.
func number(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	}
	return 0, false
}

func isNaN(f float64) bool { return math.IsNaN(f) }

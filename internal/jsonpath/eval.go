package jsonpath

import (
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// expr is a condition in a filter.
type expr interface{ truth(root, current any) bool }

type (
	orExpr  struct{ l, r expr }
	andExpr struct{ l, r expr }
	notExpr struct{ e expr }
	// existsExpr is a bare path: true when the path finds something, even a null.
	existsExpr struct{ p pathOperand }
	cmpExpr    struct {
		left  operand
		op    string
		right operand
	}
)

func (e orExpr) truth(root, cur any) bool  { return e.l.truth(root, cur) || e.r.truth(root, cur) }
func (e andExpr) truth(root, cur any) bool { return e.l.truth(root, cur) && e.r.truth(root, cur) }
func (e notExpr) truth(root, cur any) bool { return !e.e.truth(root, cur) }
func (e existsExpr) truth(root, cur any) bool {
	_, found := e.p.value(root, cur)
	return found
}

// operand is a value in a filter.
type operand interface {
	// value returns the operand's value and whether it exists. A path that finds nothing does not exist.
	value(root, current any) (any, bool)
}

type literal struct{ v any }

func (l literal) value(any, any) (any, bool) { return l.v, true }

type regexOperand struct{ re *regexp.Regexp }

func (r regexOperand) value(any, any) (any, bool) { return r.re, true }

type pathOperand struct {
	path     *Path
	relative bool
}

func (p pathOperand) value(root, cur any) (any, bool) {
	from := root
	if p.relative {
		from = cur
	}
	vals, found := p.path.eval(root, from)
	if !found {
		return nil, false
	}
	if p.path.definite || p.path.function != nil {
		return vals[0], true
	}
	return vals, true
}

func (c cmpExpr) truth(root, cur any) bool {
	l, lok := c.left.value(root, cur)
	r, rok := c.right.value(root, cur)
	if !lok || !rok {
		switch c.op {
		case "!=", "nin", "noneof":
			return true
		}
		return false
	}
	switch c.op {
	case "==":
		return looseEqual(l, r)
	case "!=":
		return !looseEqual(l, r)
	case "<", "<=", ">", ">=":
		return ordered(c.op, l, r)
	case "=~":
		return matchesRegex(l, r)
	case "in":
		return contains(r, l)
	case "nin":
		return !contains(r, l)
	case "subsetof":
		return subsetOf(l, r)
	case "anyof":
		return anyOf(l, r)
	case "noneof":
		return !anyOf(l, r)
	case "contains":
		return containsValue(l, r)
	case opSize:
		return sizeIs(l, r)
	case "empty":
		return emptyIs(l, r)
	}
	return false
}

// looseEqual compares as Jayway does: a number equals a string that reads as the same number.
func looseEqual(a, b any) bool {
	if fa, ok := number(a); ok {
		return numberEquals(fa, b)
	}
	if fb, ok := number(b); ok {
		s, isString := a.(string)
		return isString && s == formatNumber(fb)
	}
	switch x := a.(type) {
	case []any:
		y, ok := b.([]any)
		return ok && listsEqual(x, y)
	case map[string]any:
		y, ok := b.(map[string]any)
		return ok && mapsEqual(x, y)
	}
	return reflect.DeepEqual(a, b)
}

func numberEquals(fa float64, b any) bool {
	if fb, ok := number(b); ok {
		return fa == fb
	}
	if s, ok := b.(string); ok {
		f, err := strconv.ParseFloat(s, 64)
		return err == nil && f == fa
	}
	return false
}

func listsEqual(x, y []any) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if !looseEqual(x[i], y[i]) {
			return false
		}
	}
	return true
}

func mapsEqual(x, y map[string]any) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		w, found := y[k]
		if !found || !looseEqual(v, w) {
			return false
		}
	}
	return true
}

func formatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// ordered compares two numbers or two strings. Mixed types never compare.
func ordered(op string, a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		if !ok || isNaN(fa) || isNaN(fb) {
			return false
		}
		return cmpResult(op, fa < fb, fa == fb)
	}
	if sa, ok := a.(string); ok {
		sb, ok := b.(string)
		if !ok {
			return false
		}
		return cmpResult(op, sa < sb, sa == sb)
	}
	return false
}

func cmpResult(op string, less, equal bool) bool {
	switch op {
	case "<":
		return less
	case "<=":
		return less || equal
	case ">":
		return !less && !equal
	default:
		return !less
	}
}

// matchesRegex reports whether the whole text of a string or number matches. Any other value is
// matched as an empty string.
func matchesRegex(v, pattern any) bool {
	re, ok := pattern.(*regexp.Regexp)
	if !ok {
		return false
	}
	switch t := v.(type) {
	case string:
		return re.MatchString(t)
	case float64:
		return re.MatchString(formatNumber(t))
	}
	return re.MatchString("")
}

func contains(list, v any) bool {
	arr, ok := list.([]any)
	if !ok {
		return false
	}
	for _, e := range arr {
		if looseEqual(v, e) {
			return true
		}
	}
	return false
}

func subsetOf(l, r any) bool {
	left, ok := l.([]any)
	if !ok {
		return false
	}
	for _, e := range left {
		if !contains(r, e) {
			return false
		}
	}
	return true
}

func anyOf(l, r any) bool {
	left, ok := l.([]any)
	if !ok {
		return false
	}
	for _, e := range left {
		if contains(r, e) {
			return true
		}
	}
	return false
}

func containsValue(l, r any) bool {
	switch t := l.(type) {
	case string:
		s, ok := r.(string)
		return ok && strings.Contains(t, s)
	case []any:
		return contains(t, r)
	}
	return false
}

func sizeIs(l, r any) bool {
	want, ok := number(r)
	if !ok {
		return false
	}
	switch t := l.(type) {
	case []any:
		return float64(len(t)) == want
	case string:
		return float64(len(utf16.Encode([]rune(t)))) == want
	}
	return false
}

func emptyIs(l, r any) bool {
	want, ok := r.(bool)
	if !ok {
		return false
	}
	switch t := l.(type) {
	case []any:
		return (len(t) == 0) == want
	case string:
		return (t == "") == want
	}
	return false
}

// function is a path function such as size() or keys(). It ends a path.
type function struct {
	name string
	args []operand
}

func knownFunction(name string) bool {
	switch name {
	case "length", opSize, "keys", "min", "max", "avg", "sum", "stddev", "first", "last", "index", "concat", "append":
		return true
	}
	return false
}

const opSize = "size"

// call applies the function to what the path found. It returns false when the function has no result
// (Jayway returns null), which counts as not found.
func (f *function) call(root, in any, definite bool) (any, bool) {
	switch f.name {
	case "length", opSize:
		switch t := in.(type) {
		case []any:
			return float64(len(t)), true
		case map[string]any:
			return float64(len(t)), true
		}
		return nil, false
	case "keys":
		m, ok := in.(map[string]any)
		if !ok {
			return nil, false
		}
		keys := sortedKeys(m)
		out := make([]any, len(keys))
		for i, k := range keys {
			out[i] = k
		}
		return out, true
	case "first", "last", "index":
		return f.pick(root, in)
	case "concat":
		return f.concat(root, in)
	case "append":
		arr, ok := in.([]any)
		if !ok {
			return nil, false
		}
		out := append([]any{}, arr...)
		for _, a := range f.args {
			if v, ok := a.value(root, root); ok {
				out = append(out, v)
			}
		}
		return out, true
	}
	if !definite {
		return nil, false // Jayway gives no number for an aggregate over a wildcard result
	}
	return aggregate(f.name, in)
}

func (f *function) pick(root, in any) (any, bool) {
	arr, ok := in.([]any)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	switch f.name {
	case "first":
		return arr[0], true
	case "last":
		return arr[len(arr)-1], true
	}
	if len(f.args) != 1 {
		return nil, false
	}
	v, _ := f.args[0].value(root, root)
	n, ok := number(v)
	if !ok {
		return nil, false
	}
	i := int(n)
	if i < 0 {
		i += len(arr)
	}
	if i < 0 || i >= len(arr) {
		return nil, false
	}
	return arr[i], true
}

func (f *function) concat(root, in any) (any, bool) {
	switch t := in.(type) {
	case string:
		var b strings.Builder
		b.WriteString(t)
		for _, a := range f.args {
			if v, ok := a.value(root, root); ok {
				b.WriteString(toText(v))
			}
		}
		return b.String(), true
	case []any:
		out := append([]any{}, t...)
		for _, a := range f.args {
			if v, ok := a.value(root, root); ok {
				out = append(out, v)
			}
		}
		return out, true
	}
	return nil, false
}

func toText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return formatNumber(t)
	}
	return ""
}

func aggregate(name string, in any) (any, bool) {
	arr, ok := in.([]any)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	nums := make([]float64, 0, len(arr))
	for _, e := range arr {
		f, ok := number(e)
		if !ok {
			return nil, false
		}
		nums = append(nums, f)
	}
	sum := 0.0
	for _, f := range nums {
		sum += f
	}
	switch name {
	case "sum":
		return sum, true
	case "avg":
		return sum / float64(len(nums)), true
	case "min":
		sorted := append([]float64{}, nums...)
		sort.Float64s(sorted)
		return sorted[0], true
	case "max":
		sorted := append([]float64{}, nums...)
		sort.Float64s(sorted)
		return sorted[len(sorted)-1], true
	case "stddev":
		mean := sum / float64(len(nums))
		var sq float64
		for _, f := range nums {
			sq += (f - mean) * (f - mean)
		}
		return math.Sqrt(sq / float64(len(nums))), true
	}
	return nil, false
}

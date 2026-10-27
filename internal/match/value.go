package match

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/pkg/extension"

	"github.com/pwgith/shadow-mock/internal/model"
)

// Result is the outcome of one match. Distance is 0 for a match and up to 1 for a complete
// miss; near misses are ranked by it (FR-5.3).
type Result struct {
	Matched  bool
	Distance float64
}

// Hit and Miss are the two plain results.
func hit() Result  { return Result{Matched: true} }
func miss() Result { return Result{Distance: 1} }

func boolResult(ok bool) Result {
	if ok {
		return hit()
	}
	return miss()
}

// Options are the inputs a compiled matcher needs at evaluation time.
type Options struct {
	// Now returns the current time, for relative date matchers ("now +3 days").
	Now func() time.Time
	// CustomMatchers are the matchers registered through pkg/extension, by name.
	CustomMatchers map[string]extension.RequestMatcher
}

func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Unix(0, 0).UTC()
	}
	return o.Now()
}

// StringMatcher matches one optional string value. present is false when the value is absent.
type StringMatcher interface {
	MatchString(v string, present bool) Result
}

// stringFunc adapts a function to StringMatcher.
type stringFunc func(v string, present bool) Result

// MatchString calls f.
func (f stringFunc) MatchString(v string, present bool) Result { return f(v, present) }

// Logical operators.
const (
	opAnd = "and"
	opOr  = "or"
	opNot = "not"
)

// valueOperators are the string operators, in the order they are looked for.
func valueOperators() []string {
	return []string{
		"equalTo", "contains", "doesNotContain", "matches", "doesNotMatch", "absent",
		"before", "after", "equalToDateTime",
		"equalToNumber", "greaterThanNumber", "greaterThanEqualNumber", "lessThanNumber", "lessThanEqualNumber",
		opAnd, opOr, opNot,
	}
}

// CompileString compiles a StringValuePattern.
func CompileString(p model.ValuePattern, o Options) (StringMatcher, error) {
	op, ok := operator(p, valueOperators())
	if !ok {
		return nil, fmt.Errorf("unknown matcher in %s", keys(p))
	}
	switch op {
	case opAnd, opOr:
		return compileLogical(op, p[op], o)
	case opNot:
		var inner model.ValuePattern
		if err := json.Unmarshal(p[op], &inner); err != nil {
			return nil, fmt.Errorf("not: %w", err)
		}
		m, err := CompileString(inner, o)
		if err != nil {
			return nil, fmt.Errorf("not: %w", err)
		}
		return stringFunc(func(v string, present bool) Result {
			return boolResult(!m.MatchString(v, present).Matched)
		}), nil
	case "absent":
		var absent bool
		if err := json.Unmarshal(p[op], &absent); err != nil {
			return nil, fmt.Errorf("absent must be a boolean: %w", err)
		}
		return stringFunc(func(_ string, present bool) Result { return boolResult(present != absent) }), nil
	case "before", "after", "equalToDateTime":
		return compileDate(op, p, o)
	case "equalToNumber", "greaterThanNumber", "greaterThanEqualNumber", "lessThanNumber", "lessThanEqualNumber":
		return compileNumber(op, p[op])
	default:
		return compileText(op, p)
	}
}

func compileLogical(op string, raw json.RawMessage, o Options) (StringMatcher, error) {
	var parts []model.ValuePattern
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("%s must be an array of matchers: %w", op, err)
	}
	ms := make([]StringMatcher, 0, len(parts))
	for _, part := range parts {
		m, err := CompileString(part, o)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		ms = append(ms, m)
	}
	return stringFunc(func(v string, present bool) Result {
		for _, m := range ms {
			matched := m.MatchString(v, present).Matched
			if op == opAnd && !matched {
				return miss()
			}
			if op == opOr && matched {
				return hit()
			}
		}
		return boolResult(op == opAnd)
	}), nil
}

// equalTo matches exactly, or ignoring case. A near miss is scored by text similarity.
func equalTo(want string, fold bool) StringMatcher {
	return stringFunc(func(v string, present bool) Result {
		switch {
		case !present:
			return miss()
		case fold:
			return boolResult(strings.EqualFold(v, want))
		case v == want:
			return hit()
		default:
			return Result{Distance: textDistance(v, want)}
		}
	})
}

func compileText(op string, p model.ValuePattern) (StringMatcher, error) {
	var want string
	if err := json.Unmarshal(p[op], &want); err != nil {
		return nil, fmt.Errorf("%s must be a string: %w", op, err)
	}
	var fold bool
	if raw, ok := p["caseInsensitive"]; ok {
		if err := json.Unmarshal(raw, &fold); err != nil {
			return nil, fmt.Errorf("caseInsensitive must be a boolean: %w", err)
		}
	}
	switch op {
	case "equalTo":
		return equalTo(want, fold), nil
	case "contains", "doesNotContain":
		negate := op == "doesNotContain"
		return stringFunc(func(v string, present bool) Result {
			if !present {
				return boolResult(negate)
			}
			return boolResult(strings.Contains(v, want) != negate)
		}), nil
	default: // matches, doesNotMatch
		re, err := regexp.Compile(`^(?:` + want + `)$`)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid regular expression: %w", op, err)
		}
		negate := op == "doesNotMatch"
		return stringFunc(func(v string, present bool) Result {
			if !present {
				return boolResult(negate)
			}
			return boolResult(re.MatchString(v) != negate)
		}), nil
	}
}

func compileNumber(op string, raw json.RawMessage) (StringMatcher, error) {
	want, err := numberFrom(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be a number: %w", op, err)
	}
	cmp := map[string]func(a float64) bool{
		"equalToNumber":          func(a float64) bool { return a == want },
		"greaterThanNumber":      func(a float64) bool { return a > want },
		"greaterThanEqualNumber": func(a float64) bool { return a >= want },
		"lessThanNumber":         func(a float64) bool { return a < want },
		"lessThanEqualNumber":    func(a float64) bool { return a <= want },
	}[op]
	return stringFunc(func(v string, present bool) Result {
		if !present {
			return miss()
		}
		got, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return boolResult(err == nil && cmp(got))
	}), nil
}

// numberFrom accepts a JSON number or a numeric string.
func numberFrom(raw json.RawMessage) (float64, error) {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, errors.New("not a number")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return f, nil
}

// operator finds the first operator key present in p.
func operator(p model.ValuePattern, ops []string) (string, bool) {
	for _, op := range ops {
		if _, ok := p[op]; ok {
			return op, true
		}
	}
	return "", false
}

func keys(p model.ValuePattern) string {
	ks := make([]string, 0, len(p))
	for k := range p {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return "{" + strings.Join(ks, ", ") + "}"
}

// textDistance is a cheap similarity measure for near misses: the share of the longer string
// not covered by a common prefix and suffix.
func textDistance(a, b string) float64 {
	if a == b {
		return 0
	}
	longest := max(len(a), len(b))
	if longest == 0 {
		return 0
	}
	common := 0
	for common < len(a) && common < len(b) && a[common] == b[common] {
		common++
	}
	suffix := 0
	for suffix < len(a)-common && suffix < len(b)-common && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	d := 1 - float64(common+suffix)/float64(longest)
	return min(max(d, 0.05), 1)
}

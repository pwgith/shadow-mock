package match

import (
	"encoding/json"
	"fmt"

	"github.com/pwgith/shadow-mock/internal/model"
)

// MultiMatcher matches all the values of a header, query parameter or form field.
type MultiMatcher interface {
	MatchValues(vs []string) Result
}

type multiFunc func(vs []string) Result

// MatchValues calls f.
func (f multiFunc) MatchValues(vs []string) Result { return f(vs) }

// CompileMulti compiles a MultiValuePattern: a single string pattern (matched if any value
// matches, or for absent if there are none), or hasExactly / includes.
func CompileMulti(p model.ValuePattern, o Options) (MultiMatcher, error) {
	for _, op := range []string{"hasExactly", "includes"} {
		raw, ok := p[op]
		if !ok {
			continue
		}
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
		if op == "hasExactly" {
			return multiFunc(func(vs []string) Result { return hasExactly(ms, vs) }), nil
		}
		return multiFunc(func(vs []string) Result { return boolResult(eachMatchesSome(ms, vs)) }), nil
	}
	single, err := CompileString(p, o)
	if err != nil {
		return nil, err
	}
	return multiFunc(func(vs []string) Result {
		if len(vs) == 0 {
			return single.MatchString("", false)
		}
		best := miss()
		for _, v := range vs {
			r := single.MatchString(v, true)
			if r.Matched {
				return r
			}
			best.Distance = min(best.Distance, r.Distance)
		}
		return best
	}), nil
}

func eachMatchesSome(ms []StringMatcher, vs []string) bool {
	for _, m := range ms {
		found := false
		for _, v := range vs {
			if m.MatchString(v, true).Matched {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// hasExactly matches when every pattern matches a value and every value is matched.
func hasExactly(ms []StringMatcher, vs []string) Result {
	if len(ms) != len(vs) || !eachMatchesSome(ms, vs) {
		return miss()
	}
	for _, v := range vs {
		matched := false
		for _, m := range ms {
			if m.MatchString(v, true).Matched {
				matched = true
				break
			}
		}
		if !matched {
			return miss()
		}
	}
	return hit()
}

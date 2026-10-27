package model

import (
	"strconv"
	"strings"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

// CallResponse is one callResponses rule as written: exactly one of Calls or Every.
type CallResponse struct {
	Calls    string              `json:"calls,omitempty"`
	Every    int64               `json:"every,omitempty"`
	Response *ResponseDefinition `json:"response"`
}

// CallRule is a parsed callResponses rule (DESIGN.md §6.3).
type CallRule struct {
	// From and To are inclusive call numbers. To is 0 for an open range ("100+").
	From, To int64
	// Every is n for an "every n" rule, otherwise 0.
	Every    int64
	Response ResponseDefinition
}

// Matches reports whether the rule selects call n (n starts at 1).
func (r *CallRule) Matches(n int64) bool {
	if r.Every > 0 {
		return n%r.Every == 0
	}
	return n >= r.From && (r.To == 0 || n <= r.To)
}

func compileCallRules(in []CallResponse) ([]CallRule, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]CallRule, 0, len(in))
	for i := range in {
		ptr := pointerJoin("/callResponses", strconv.Itoa(i))
		if in[i].Response == nil {
			return nil, apierr.Invalid(ptr, "response is required")
		}
		rule, err := compileCallRule(ptr, &in[i])
		if err != nil {
			return nil, err
		}
		if err := rule.Response.Validate(ptr + "/response"); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

func compileCallRule(ptr string, c *CallResponse) (CallRule, error) {
	switch {
	case c.Calls != "" && c.Every != 0:
		return CallRule{}, apierr.Invalid(ptr, "set exactly one of calls and every")
	case c.Every != 0:
		if c.Every < 2 {
			return CallRule{}, apierr.Invalid(ptr+"/every", "every must be 2 or more")
		}
		return CallRule{Every: c.Every, Response: *c.Response}, nil
	case c.Calls != "":
		from, to, err := parseCalls(c.Calls)
		if err != nil {
			return CallRule{}, apierr.Invalid(ptr+"/calls", "%s", err.Error())
		}
		return CallRule{From: from, To: to, Response: *c.Response}, nil
	default:
		return CallRule{}, apierr.Invalid(ptr, "set exactly one of calls and every")
	}
}

// callsError explains an invalid calls selector.
type callsError struct{ calls, why string }

func (e *callsError) Error() string {
	return `calls "` + e.calls + `" ` + e.why + `; use "3", "1-2" or "100+"`
}

// parseCalls parses "3", "1-2" or "100+".
func parseCalls(s string) (from, to int64, err error) {
	switch {
	case strings.HasSuffix(s, "+"):
		n, ok := positive(strings.TrimSuffix(s, "+"))
		if !ok {
			return 0, 0, &callsError{s, "is not a call number"}
		}
		return n, 0, nil
	case strings.Contains(s, "-"):
		a, b, _ := strings.Cut(s, "-")
		lo, okA := positive(a)
		hi, okB := positive(b)
		if !okA || !okB {
			return 0, 0, &callsError{s, "is not a range of call numbers"}
		}
		if hi < lo {
			return 0, 0, &callsError{s, "runs backwards"}
		}
		return lo, hi, nil
	default:
		n, ok := positive(s)
		if !ok {
			return 0, 0, &callsError{s, "is not a call number"}
		}
		return n, n, nil
	}
}

func positive(s string) (int64, bool) {
	if s == "" || s[0] == '0' || s[0] == '+' || s[0] == '-' {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 1
}

package match

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/pwgith/shadow-mock/internal/model"
)

// JSON-unit placeholders supported by equalToJson (FR-2.5).
const (
	placeholderPrefix = "${json-unit."
	phIgnore          = "${json-unit.ignore}"
	phAnyString       = "${json-unit.any-string}"
	phAnyNumber       = "${json-unit.any-number}"
	phAnyBoolean      = "${json-unit.any-boolean}"
	phRegex           = "${json-unit.regex}"
)

// jsonEqual implements equalToJson.
type jsonEqual struct {
	want               any
	ignoreArrayOrder   bool
	ignoreExtraElement bool
	regexes            map[string]*regexp.Regexp
}

func compileEqualToJSON(p model.ValuePattern) (*jsonEqual, error) {
	raw := p["equalToJson"]
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		raw = json.RawMessage(asString)
	}
	want, ok := parseJSON(raw)
	if !ok {
		return nil, errors.New("equalToJson: expected value is not valid JSON")
	}
	m := &jsonEqual{want: want, regexes: map[string]*regexp.Regexp{}}
	for field, dst := range map[string]*bool{"ignoreArrayOrder": &m.ignoreArrayOrder, "ignoreExtraElements": &m.ignoreExtraElement} {
		if r, ok := p[field]; ok {
			if err := json.Unmarshal(r, dst); err != nil {
				return nil, fmt.Errorf("%s must be a boolean: %w", field, err)
			}
		}
	}
	if err := m.collectRegexes(want); err != nil {
		return nil, err
	}
	return m, nil
}

// collectRegexes compiles every ${json-unit.regex} placeholder once.
func (m *jsonEqual) collectRegexes(v any) error {
	switch t := v.(type) {
	case string:
		if expr, ok := strings.CutPrefix(t, phRegex); ok {
			re, err := regexp.Compile("^(?:" + expr + ")$")
			if err != nil {
				return fmt.Errorf("equalToJson: invalid regex placeholder: %w", err)
			}
			m.regexes[t] = re
		}
	case map[string]any:
		for _, x := range t {
			if err := m.collectRegexes(x); err != nil {
				return err
			}
		}
	case []any:
		for _, x := range t {
			if err := m.collectRegexes(x); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *jsonEqual) matchValue(actual any) Result { return boolResult(m.equal(m.want, actual)) }

func (m *jsonEqual) equal(want, got any) bool {
	if s, ok := want.(string); ok && strings.HasPrefix(s, placeholderPrefix) {
		return m.placeholder(s, got)
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		return ok && m.equalObject(w, g)
	case []any:
		g, ok := got.([]any)
		return ok && m.equalArray(w, g)
	default:
		return want == got
	}
}

func (m *jsonEqual) placeholder(s string, got any) bool {
	switch s {
	case phIgnore:
		return true
	case phAnyString:
		_, ok := got.(string)
		return ok
	case phAnyNumber:
		_, ok := got.(float64)
		return ok
	case phAnyBoolean:
		_, ok := got.(bool)
		return ok
	}
	if re, ok := m.regexes[s]; ok {
		g, isString := got.(string)
		return isString && re.MatchString(g)
	}
	return s == got
}

func (m *jsonEqual) equalObject(want, got map[string]any) bool {
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			if s, isString := wv.(string); isString && s == phIgnore {
				continue
			}
			return false
		}
		if !m.equal(wv, gv) {
			return false
		}
	}
	if m.ignoreExtraElement {
		return true
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			return false
		}
	}
	return true
}

func (m *jsonEqual) equalArray(want, got []any) bool {
	if len(got) != len(want) && (!m.ignoreExtraElement || len(got) < len(want)) {
		return false
	}
	if !m.ignoreArrayOrder {
		for i := range want {
			if !m.equal(want[i], got[i]) {
				return false
			}
		}
		return true
	}
	used := make([]bool, len(got))
	for _, wv := range want {
		found := false
		for j, gv := range got {
			if !used[j] && m.equal(wv, gv) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

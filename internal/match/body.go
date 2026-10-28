package match

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/pwgith/shadow-mock/internal/jsonpath"
	"github.com/pwgith/shadow-mock/internal/model"
)

// Content is a body (or other document, such as mapping metadata) to match.
type Content struct {
	Bytes []byte
	// JSON returns the content parsed as JSON. It is computed lazily and cached by the caller.
	JSON func() (any, bool)
}

// ContentOf wraps bytes as Content with its own JSON parsing.
func ContentOf(b []byte) Content {
	var (
		parsed bool
		v      any
		ok     bool
	)
	return Content{Bytes: b, JSON: func() (any, bool) {
		if !parsed {
			v, ok = parseJSON(b)
			parsed = true
		}
		return v, ok
	}}
}

// ContentMatcher matches a body.
type ContentMatcher interface {
	MatchContent(c Content) Result
}

type contentFunc func(c Content) Result

// MatchContent calls f.
func (f contentFunc) MatchContent(c Content) Result { return f(c) }

// CompileContent compiles a ContentPattern: a string matcher applied to the whole body, or one
// of the structured body operators.
func CompileContent(p model.ValuePattern, o Options) (ContentMatcher, error) {
	switch {
	case has(p, "equalToJson"):
		m, err := compileEqualToJSON(p)
		if err != nil {
			return nil, err
		}
		return contentFunc(func(c Content) Result {
			v, ok := c.JSON()
			if !ok {
				return miss()
			}
			return m.matchValue(v)
		}), nil
	case has(p, "matchesJsonPath"):
		return compileJSONPath(p["matchesJsonPath"], o)
	case has(p, "matchesJsonSchema"):
		return compileJSONSchema(p)
	case has(p, "binaryEqualTo"):
		var s string
		if err := json.Unmarshal(p["binaryEqualTo"], &s); err != nil {
			return nil, fmt.Errorf("binaryEqualTo must be a base64 string: %w", err)
		}
		want, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("binaryEqualTo is not valid base64: %w", err)
		}
		return contentFunc(func(c Content) Result { return boolResult(bytes.Equal(c.Bytes, want)) }), nil
	case has(p, "equalToXml"):
		return compileEqualToXML(p)
	case has(p, "matchesXPath"):
		return compileXPath(p, o)
	}
	sm, err := CompileString(p, o)
	if err != nil {
		return nil, err
	}
	return contentFunc(func(c Content) Result { return sm.MatchString(string(c.Bytes), len(c.Bytes) > 0) }), nil
}

func has(p model.ValuePattern, k string) bool {
	_, ok := p[k]
	return ok
}

// compileJSONPath implements matchesJsonPath: a bare expression matches when it finds a
// non-empty result; with a sub-matcher, the result is matched as text or JSON.
func compileJSONPath(raw json.RawMessage, o Options) (ContentMatcher, error) {
	var expr string
	var sub ContentMatcher
	if strErr := json.Unmarshal(raw, &expr); strErr != nil {
		var obj model.ValuePattern
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("matchesJsonPath must be a string or an object: %w", err)
		}
		if err := json.Unmarshal(obj["expression"], &expr); err != nil {
			return nil, fmt.Errorf("matchesJsonPath needs an expression: %w", err)
		}
		delete(obj, "expression")
		compiled, err := CompileContent(obj, o)
		if err != nil {
			return nil, fmt.Errorf("matchesJsonPath %s: %w", expr, err)
		}
		sub = compiled
	}
	x, err := jsonpath.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("matchesJsonPath: invalid expression %q: %w", expr, err)
	}
	return contentFunc(func(c Content) Result {
		doc, ok := c.JSON()
		if !ok {
			return miss()
		}
		results := x.Get(doc)
		if sub == nil {
			return boolResult(x.Matches(doc))
		}
		return matchJSONPathResults(sub, results)
	}), nil
}

func matchJSONPathResults(sub ContentMatcher, results []any) Result {
	if len(results) == 0 {
		return sub.MatchContent(ContentOf(nil))
	}
	best := miss()
	candidates := results
	if len(results) > 1 {
		candidates = append(candidates, results)
	}
	for _, r := range candidates {
		res := sub.MatchContent(ContentOf(renderValue(r)))
		if res.Matched {
			return res
		}
		best.Distance = min(best.Distance, res.Distance)
	}
	return best
}

// renderValue prints a JSON value as WireMock does for sub-matchers: strings unquoted,
// numbers without a trailing ".0", and everything else as JSON.
func renderValue(v any) []byte {
	switch t := v.(type) {
	case string:
		return []byte(t)
	case float64:
		return []byte(strconv.FormatFloat(t, 'f', -1, 64))
	case nil:
		return nil
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return nil
		}
		return b
	}
}

func compileJSONSchema(p model.ValuePattern) (ContentMatcher, error) {
	raw := p["matchesJsonSchema"]
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		raw = json.RawMessage(asString)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("matchesJsonSchema: the schema is not valid JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	if v, ok := p["schemaVersion"]; ok {
		var name string
		if json.Unmarshal(v, &name) == nil {
			if d := draft(name); d != nil {
				c.DefaultDraft(d)
			}
		}
	}
	const loc = "mem:///schema.json"
	if err = c.AddResource(loc, doc); err != nil {
		return nil, fmt.Errorf("matchesJsonSchema: %w", err)
	}
	sch, err := c.Compile(loc)
	if err != nil {
		return nil, fmt.Errorf("matchesJsonSchema: invalid schema: %w", err)
	}
	return contentFunc(func(c Content) Result {
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(c.Bytes))
		if err != nil {
			return miss()
		}
		return boolResult(sch.Validate(v) == nil)
	}), nil
}

func draft(name string) *jsonschema.Draft {
	switch name {
	case "V4":
		return jsonschema.Draft4
	case "V6":
		return jsonschema.Draft6
	case "V7":
		return jsonschema.Draft7
	case "V201909":
		return jsonschema.Draft2019
	case "V202012":
		return jsonschema.Draft2020
	default:
		return nil
	}
}

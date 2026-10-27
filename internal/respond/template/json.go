package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mailgun/raymond/v2"
	"github.com/ohler55/ojg/jp"
)

// jsonHelpers are WireMock's JSON helpers beyond jsonPath and parseJson.
func jsonHelpers() map[string]any {
	return map[string]any{
		"toJson":       func(v any, _ *raymond.Options) any { return safe(string(mustJSON(v))) },
		"formatJson":   formatJSON,
		"jsonMerge":    jsonMerge,
		"jsonRemove":   jsonRemove,
		"jsonArrayAdd": jsonArrayAdd,
		"regexGroups":  regexGroups,
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(plain(v))
	if err != nil {
		fail("cannot write %T as JSON: %v", v, err)
	}
	return b
}

// plain turns template values (safe strings, multi-values, path values) into plain JSON values.
func plain(v any) any {
	switch t := v.(type) {
	case raymond.SafeString:
		return string(t)
	case []any:
		return plainList(t)
	case map[string]any:
		return plainMap(t)
	case jsonObject:
		return plainMap(t)
	default:
		return v
	}
}

// formatJSON implements {{formatJson doc format='pretty'|'compact'}}; pretty is the default.
func formatJSON(doc any, o *raymond.Options) any {
	var v any
	if err := json.Unmarshal([]byte(text(plainText(doc))), &v); err != nil {
		v = plain(doc)
	}
	if o.HashStr("format") == "compact" {
		return safe(string(mustJSON(v)))
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fail("formatJson: %v", err)
	}
	return safe(string(bytes.TrimRight(b.Bytes(), "\n")))
}

// plainText returns v if it is JSON text, else nil so callers fall back to the value itself.
func plainText(v any) any {
	switch v.(type) {
	case string, raymond.SafeString:
		return v
	default:
		return nil
	}
}

// jsonMerge implements {{jsonMerge a b removeNulls=true}}: b is merged over a, recursively.
func jsonMerge(a, b any, o *raymond.Options) any {
	merged := merge(plain(document(a)), plain(document(b)))
	if truthy(o.HashProp("removeNulls")) {
		merged = dropNulls(merged)
	}
	return safe(string(mustJSON(merged)))
}

func merge(a, b any) any {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok {
		return b
	}
	out := make(map[string]any, len(am)+len(bm))
	for k, v := range am {
		out[k] = v
	}
	for k, v := range bm {
		if old, ok := out[k]; ok {
			out[k] = merge(old, v)
		} else {
			out[k] = v
		}
	}
	return out
}

func dropNulls(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if x != nil {
				out[k] = dropNulls(x)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			if x != nil {
				out = append(out, dropNulls(x))
			}
		}
		return out
	default:
		return v
	}
}

// jsonRemove implements {{jsonRemove doc 'jsonPath'}}.
func jsonRemove(doc, expr any, _ *raymond.Options) any {
	x, err := parsePath(expr)
	if err != nil {
		fail("jsonRemove: invalid expression %q: %v", text(expr), err)
	}
	out, err := x.Remove(plain(document(doc)))
	if err != nil {
		fail("jsonRemove: %v", err)
	}
	return safe(string(mustJSON(out)))
}

// jsonArrayAdd implements {{jsonArrayAdd array item maxItems=3 flatten=true}}: the item is
// appended, and the oldest items dropped to stay within maxItems.
func jsonArrayAdd(arr, item any, o *raymond.Options) any {
	var list []any
	switch v := plain(document(arr)).(type) {
	case []any:
		list = v
	case nil: // a missing array starts empty
	default:
		fail("jsonArrayAdd: the first parameter must be a JSON array")
	}
	add := plain(document(item))
	if more, ok := add.([]any); ok && truthy(o.HashProp("flatten")) {
		list = append(list, more...)
	} else {
		list = append(list, add)
	}
	if n, ok := number(o.HashProp("maxItems")); ok && int(n) < len(list) && n >= 0 {
		list = list[len(list)-int(n):]
	}
	return safe(string(mustJSON(list)))
}

// regexGroups returns the capture groups of the first match, for the assigning form of
// regexExtract. It is empty when the pattern does not match.
func regexGroups(s, pattern any, _ *raymond.Options) any {
	re, err := regexp.Compile(text(pattern))
	if err != nil {
		fail("regexExtract: %v", err)
	}
	m := re.FindStringSubmatch(text(s))
	out := []any{}
	if len(m) > 1 {
		for _, g := range m[1:] {
			out = append(out, safe(g))
		}
	} else if len(m) == 1 {
		out = append(out, safe(m[0]))
	}
	return out
}

// parsePath parses a JSONPath expression. Jayway-style ".[" after a dot, which WireMock
// accepts, is read as "[".
func parsePath(expr any) (jp.Expr, error) {
	x, err := jp.ParseString(strings.ReplaceAll(text(expr), ".[", "["))
	if err != nil {
		return nil, fmt.Errorf("parse JSONPath: %w", err)
	}
	return x, nil
}

// jsonObject is a JSON object held in the template context. It prints as JSON and is read
// field by field like the map it is. Lists stay plain slices, because raymond prints a slice
// element by element and iterates it; use toJson to print one.
type jsonObject map[string]any

// String implements fmt.Stringer.
func (o jsonObject) String() string { return Mark(string(mustJSON(map[string]any(o)))) }

// wrap marks the strings in a parsed JSON value as safe, and makes an object print as JSON.
func wrap(v any) any {
	switch t := Safe(v).(type) {
	case map[string]any:
		return jsonObject(t)
	default:
		return t
	}
}

func plainList(t []any) []any {
	out := make([]any, len(t))
	for i, x := range t {
		out[i] = plain(x)
	}
	return out
}

func plainMap(t map[string]any) map[string]any {
	out := make(map[string]any, len(t))
	for k, x := range t {
		out[k] = plain(x)
	}
	return out
}

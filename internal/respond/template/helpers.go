package template

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/antchfx/xmlquery"
	"github.com/mailgun/raymond/v2"

	"github.com/pwgith/shadow-mock/internal/xmlguard"
)

// helpers returns WireMock's helper set. raymond fixes each helper's number of positional
// parameters by its Go signature, so every helper here takes the parameters WireMock's
// documentation gives it, followed by the options that carry the hash (name=value pairs).
// Only array, and and or take any number of parameters.
func (e *Engine) helpers() map[string]any {
	h := map[string]any{
		// Dates and times.
		"now":          e.now,
		"formatDate":   formatDateHelper,
		"parseDate":    parseDateHelper,
		"truncateDate": truncateDateHelper,
		// Random values.
		"randomValue": randomValue,
		"randomInt":   randomInt,
		// Strings and encodings.
		"base64":       base64Helper,
		"urlEncode":    func(s any, _ *raymond.Options) any { return safe(url.QueryEscape(text(s))) },
		"regexExtract": regexExtract,
		"trim":         func(s any, _ *raymond.Options) any { return safe(strings.TrimSpace(text(s))) },
		"lower":        func(s any, _ *raymond.Options) any { return safe(strings.ToLower(text(s))) },
		"upper":        func(s any, _ *raymond.Options) any { return safe(strings.ToUpper(text(s))) },
		"capitalize":   capitalize,
		"replace": func(s, old, repl any, _ *raymond.Options) any {
			return safe(strings.ReplaceAll(text(s), text(old), text(repl)))
		},
		"substring": substring,
		"join":      join,
		"size":      size,
		// Documents.
		"jsonPath":  jsonPath,
		"xPath":     xPath,
		"parseJson": func(doc any, _ *raymond.Options) any { return wrap(document(doc)) },
		// Collections and arithmetic.
		"array": func(items ...any) any { return items },
		"math":  mathHelper,
		// Comparison, for use in subexpressions such as (eq a b).
		"eq":        func(a, b any, _ *raymond.Options) any { return compare(a, b) == 0 },
		"neq":       func(a, b any, _ *raymond.Options) any { return compare(a, b) != 0 },
		"gt":        func(a, b any, _ *raymond.Options) any { return compare(a, b) > 0 },
		"gte":       func(a, b any, _ *raymond.Options) any { return compare(a, b) >= 0 },
		"lt":        func(a, b any, _ *raymond.Options) any { return compare(a, b) < 0 },
		"lte":       func(a, b any, _ *raymond.Options) any { return compare(a, b) <= 0 },
		"contains":  func(a, b any, _ *raymond.Options) any { return strings.Contains(text(a), text(b)) },
		"and":       func(items ...any) any { return all(items, true) },
		"or":        func(items ...any) any { return all(items, false) },
		"not":       func(v any, _ *raymond.Options) any { return !truthy(v) },
		"matches":   matchesHelper,
		"val":       val,
		"arrayAdd":  arrayAdd,
		"arrayJoin": arrayJoin,
		"range":     rangeHelper,
		// closeBrace writes the literal brace that follows a tag, as in {"a": {{x}}}; see rewrite.
		"closeBrace": func(_ *raymond.Options) any { return safe("}") },
	}
	maps.Copy(h, blockHelpers())
	maps.Copy(h, jsonHelpers())
	maps.Copy(h, moreHelpers())
	return h
}

func safe(s string) raymond.SafeString { return raymond.SafeString(s) }

// text renders a template value the way the engine prints it.
func text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case raymond.SafeString:
		return string(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case fmt.Stringer:
		return strings.NewReplacer(markStart, "", markEnd, "").Replace(t.String())
	case []any, map[string]any:
		b, err := marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	default:
		return fmt.Sprint(t)
	}
}

func number(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case bool, nil:
		return 0, false
	default:
		f, err := strconv.ParseFloat(strings.TrimSpace(text(t)), 64)
		return f, err == nil
	}
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string, raymond.SafeString:
		return text(t) != ""
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	default:
		return true
	}
}

// all reports whether every item is truthy (want true) or falsy (want false).
func all(items []any, want bool) bool {
	for _, it := range items {
		if truthy(it) != want {
			return !want
		}
	}
	return want
}

// compare orders two values numerically when both are numbers, else as text.
func compare(a, b any) int {
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			default:
				return 0
			}
		}
	}
	return strings.Compare(text(a), text(b))
}

// now implements {{now}} with the format, offset and timezone options.
func (e *Engine) now(o *raymond.Options) any {
	loc, err := zone(o.HashStr("timezone"))
	if err != nil {
		fail("now: %v", err)
	}
	t, err := offsetTime(e.clock.Now().In(loc), o.HashStr("offset"))
	if err != nil {
		fail("now: %v", err)
	}
	return safe(formatTime(t, o.HashStr("format")))
}

// formatDateHelper implements {{formatDate date format=...}}.
func formatDateHelper(date any, o *raymond.Options) any {
	t, err := parseTime(text(date), o.HashStr("parseFormat"))
	if err != nil {
		fail("formatDate: %v", err)
	}
	loc, err := zone(o.HashStr("timezone"))
	if err != nil {
		fail("formatDate: %v", err)
	}
	return safe(formatTime(t.In(loc), o.HashStr("format")))
}

// parseDateHelper implements {{parseDate text format=...}}, giving an ISO 8601 date.
func parseDateHelper(s any, o *raymond.Options) any {
	t, err := parseTime(text(s), o.HashStr("format"))
	if err != nil {
		fail("parseDate: %v", err)
	}
	return safe(formatTime(t, ""))
}

// truncateDateHelper implements {{truncateDate date 'first day of month'}}-style rounding.
func truncateDateHelper(date, unit any, o *raymond.Options) any {
	t, err := parseTime(text(date), o.HashStr("parseFormat"))
	if err != nil {
		fail("truncateDate: %v", err)
	}
	t, err = truncate(t, strings.TrimSpace(text(unit)))
	if err != nil {
		fail("truncateDate: %v", err)
	}
	return safe(formatTime(t, o.HashStr("format")))
}

const (
	alphabetic   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	numeric      = "0123456789"
	hexDigits    = "0123456789abcdef"
	symbols      = "!@#$%^&*()-_=+[]{};:,.<>?"
	defaultRandN = 36
	maxRandN     = 10000
)

// randomValue implements {{randomValue length=8 type='ALPHANUMERIC'}}.
func randomValue(o *raymond.Options) any {
	typ := strings.ToUpper(o.HashStr("type"))
	if typ == "" {
		typ = "ALPHANUMERIC"
	}
	if typ == "UUID" {
		return safe(uuid())
	}
	length := defaultRandN
	if v := o.HashProp("length"); v != nil {
		n, ok := number(v)
		if !ok || n < 0 || n > maxRandN {
			fail("randomValue: invalid length %v", v)
		}
		length = int(n)
	}
	var alphabet string
	switch typ {
	case "ALPHANUMERIC":
		alphabet = alphabetic + numeric
	case "ALPHABETIC":
		alphabet = alphabetic
	case "NUMERIC":
		alphabet = numeric
	case "ALPHANUMERIC_AND_SYMBOLS":
		alphabet = alphabetic + numeric + symbols
	case "HEXADECIMAL":
		alphabet = hexDigits
	default:
		fail("randomValue: unknown type %q", typ)
	}
	s := pickChars(alphabet, length)
	if truthy(o.HashProp("uppercase")) {
		s = strings.ToUpper(s)
	}
	return safe(s)
}

// randomInt implements {{randomInt lower=1 upper=10}}.
func randomInt(o *raymond.Options) any {
	lo, hi := int64(0), int64(math.MaxInt32)
	if v, ok := number(o.HashProp("lower")); ok {
		lo = int64(v)
	}
	if v, ok := number(o.HashProp("upper")); ok {
		hi = int64(v)
	}
	if hi < lo {
		fail("randomInt: upper %d is below lower %d", hi, lo)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(hi-lo+1))
	if err != nil {
		fail("randomInt: %v", err)
	}
	return n.Int64() + lo
}

func pickChars(alphabet string, n int) string {
	var b strings.Builder
	limit := big.NewInt(int64(len(alphabet)))
	for range n {
		i, err := rand.Int(rand.Reader, limit)
		if err != nil {
			fail("random value: %v", err)
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String()
}

func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		fail("random uuid: %v", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// base64Helper encodes its parameter, or decodes it with decode=true.
func base64Helper(s any, o *raymond.Options) any {
	in := text(s)
	if truthy(o.HashProp("decode")) {
		b, err := base64.StdEncoding.DecodeString(in)
		if err != nil {
			b, err = base64.RawStdEncoding.DecodeString(in)
		}
		if err != nil {
			fail("base64: %v", err)
		}
		return safe(string(b))
	}
	enc := base64.StdEncoding
	if o.HashProp("padding") == false {
		enc = base64.RawStdEncoding
	}
	return safe(enc.EncodeToString([]byte(in)))
}

// regexExtract returns the first match of a pattern, or the default= value when there is none.
func regexExtract(s, pattern any, o *raymond.Options) any {
	re, err := regexp.Compile(text(pattern))
	if err != nil {
		fail("regexExtract: %v", err)
	}
	m := re.FindStringSubmatch(text(s))
	switch {
	case m == nil:
		return safe(o.HashStr("default"))
	case len(m) > 1:
		return safe(m[1])
	default:
		return safe(m[0])
	}
}

func capitalize(s any, _ *raymond.Options) any {
	in := text(s)
	r, n := utf8.DecodeRuneInString(in)
	return safe(strings.ToUpper(string(r)) + in[n:])
}

// substring implements {{substring text start end}}; positions count characters.
func substring(s, from, to any, _ *raymond.Options) any {
	rs := []rune(text(s))
	lo, _ := number(from)
	hi, ok := number(to)
	if !ok {
		hi = float64(len(rs))
	}
	start, end := clamp(int(lo), len(rs)), clamp(int(hi), len(rs))
	if end < start {
		return safe("")
	}
	return safe(string(rs[start:end]))
}

func clamp(i, n int) int { return max(0, min(i, n)) }

func join(items, sep any, _ *raymond.Options) any {
	list, ok := plain(items).([]any)
	if !ok {
		fail("join: the first parameter must be a list")
	}
	parts := make([]string, len(list))
	for i, it := range list {
		parts[i] = text(it)
	}
	return safe(strings.Join(parts, text(sep)))
}

func size(v any, _ *raymond.Options) any {
	switch t := v.(type) {
	case []any:
		return len(t)
	case map[string]any:
		return len(t)
	case jsonObject:
		return len(t)
	default:
		return utf8.RuneCountInString(text(t))
	}
}

// mathHelper implements {{math a '+' b}} for + - * / and %.
func mathHelper(a, op, b any, _ *raymond.Options) any {
	x, ok1 := number(a)
	y, ok2 := number(b)
	if !ok1 || !ok2 {
		fail("math: operands %v and %v must be numbers", a, b)
	}
	switch o := text(op); o {
	case "+":
		return x + y
	case "-":
		return x - y
	case "*":
		return x * y
	case "/":
		if y == 0 {
			fail("math: division by zero")
		}
		return x / y
	case "%":
		if y == 0 {
			fail("math: division by zero")
		}
		return math.Mod(x, y)
	default:
		fail("math: unknown operator %q", o)
		return nil
	}
}

// jsonPath evaluates a JSONPath over JSON text or an already parsed value.
func jsonPath(doc, expr any, o *raymond.Options) any {
	x, err := parsePath(expr)
	if err != nil {
		fail("jsonPath: invalid expression %q: %v", text(expr), err)
	}
	res := x.Get(document(doc))
	switch len(res) {
	case 0:
		return safe(o.HashStr("default")) // {{jsonPath body '$.a' default='x'}}
	case 1:
		return wrap(res[0])
	default:
		return wrap(res)
	}
}

// document turns JSON text into a value; values that are already parsed pass through.
func document(v any) any {
	switch t := v.(type) {
	case string, raymond.SafeString:
		var out any
		dec := json.NewDecoder(strings.NewReader(text(t)))
		if err := dec.Decode(&out); err != nil {
			fail("the input is not valid JSON: %v", err)
		}
		return out
	default:
		return v
	}
}

// xPath evaluates an XPath over XML text and returns the text of the first match.
func xPath(doc, expr any, _ *raymond.Options) any {
	if !xmlguard.DepthOK([]byte(text(doc))) {
		fail("xPath: the XML is nested more than %d levels deep", xmlguard.MaxDepth)
	}
	root, err := xmlquery.Parse(strings.NewReader(text(doc)))
	if err != nil {
		fail("xPath: the input is not valid XML: %v", err)
	}
	nodes, err := xmlquery.QueryAll(root, text(expr))
	if err != nil {
		fail("xPath: invalid expression %q: %v", text(expr), err)
	}
	if len(nodes) == 0 {
		return safe("")
	}
	return safe(nodes[0].InnerText())
}

// matchesHelper implements (matches text regex): true when the whole text matches. The block form
// {{#matches text regex}} is rewritten to an if around this.
func matchesHelper(s, pattern any, _ *raymond.Options) any {
	re, err := regexp.Compile("^(?:" + text(pattern) + ")$")
	if err != nil {
		fail("matches: %v", err)
	}
	return re.MatchString(text(s))
}

// val implements {{val value or=fallback}}: the value, or the fallback when it is missing or empty.
// default= is accepted as well.
func val(v any, o *raymond.Options) any {
	result := v
	if text(v) == "" {
		if fallback := o.HashProp("or"); fallback != nil {
			result = fallback
		} else {
			result = safe(o.HashStr("default"))
		}
	}
	if name := o.HashStr("assign"); name != "" {
		setVar(o, name, Safe(result))
		return safe("")
	}
	return result
}

// arrayAdd implements (arrayAdd list item position=start): a new list with the item added at the
// end, or at the start.
func arrayAdd(list, item any, o *raymond.Options) any {
	in := asList(list)
	if o.HashStr("position") == "start" {
		return append([]any{item}, in...)
	}
	return append(slices.Clone(in), item)
}

// arrayJoin implements {{arrayJoin ',' list}}.
func arrayJoin(sep, list any, _ *raymond.Options) any {
	in := asList(list)
	parts := make([]string, len(in))
	for i, x := range in {
		parts[i] = text(x)
	}
	return safe(strings.Join(parts, text(sep)))
}

// rangeHelper implements (range start end): the whole numbers from start to end, both included.
func rangeHelper(from, to any, _ *raymond.Options) any {
	a, ok1 := number(from)
	b, ok2 := number(to)
	if !ok1 || !ok2 || b < a || b-a > 100000 {
		fail("range: %v to %v is not a usable range", from, to)
	}
	out := make([]any, 0, int(b-a)+1)
	for i := a; i <= b; i++ {
		out = append(out, i)
	}
	return out
}

// asList returns a template value as a list, or nil when it is not one.
func asList(v any) []any {
	l, ok := plain(document(v)).([]any)
	if !ok {
		return nil
	}
	return l
}

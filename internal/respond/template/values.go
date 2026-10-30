package template

import (
	"math"
	"strconv"
	"strings"
)

// list is a list value that prints the way Java's List.toString does: [a, b]. It is what array,
// range, arrayAdd and arrayRemove return (FR-10.5).
type list []any

// String implements fmt.Stringer.
func (l list) String() string { return Mark(javaList(l)) }

// jsonList is a list a jsonPath helper found. WireMock prints it as compact JSON.
type jsonList []any

// String implements fmt.Stringer.
func (l jsonList) String() string { return Mark(string(mustJSON([]any(l)))) }

func javaList(items []any) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = javaString(it)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// javaString prints a value the way Java's toString does.
func javaString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case list:
		return javaList(t)
	case jsonList:
		return javaList(t)
	case []any:
		return javaList(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return text(v)
	}
}

// javaDouble prints a number the way Java's Double.toString does: "10.0", "2.5", and "1.0E7" from ten million.
func javaDouble(f float64) string {
	if f == 0 {
		return "0.0"
	}
	abs := math.Abs(f)
	if abs >= 1e-3 && abs < 1e7 {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	mantissa, exp, _ := strings.Cut(strconv.FormatFloat(f, 'E', -1, 64), "E")
	if !strings.Contains(mantissa, ".") {
		mantissa += ".0"
	}
	n, err := strconv.Atoi(exp) // "+07" is 7
	if err != nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return mantissa + "E" + strconv.Itoa(n)
}

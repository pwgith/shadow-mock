package template

import (
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mailgun/raymond/v2"
)

// The string helpers WireMock gets from Handlebars.java, and the small helpers WireMock adds
// beside them (FR-10.4).

// inlineError is how WireMock reports bad input to some helpers: the error replaces the output.
func inlineError(format string, args ...any) any {
	return safe("[ERROR: " + fmt.Sprintf(format, args...) + "]")
}

// stringHelpers returns the helpers that work on text, numbers and the host.
func (e *Engine) stringHelpers() map[string]any {
	return map[string]any{
		"abbreviate":      abbreviate,
		"capitalizeFirst": capitalizeFirst,
		"center":          func(s any, o *raymond.Options) any { return justify(s, o, centreText) },
		"ljust":           func(s any, o *raymond.Options) any { return justify(s, o, leftText) },
		"rjust":           func(s any, o *raymond.Options) any { return justify(s, o, rightText) },
		"cut":             func(s, sep any, _ *raymond.Options) any { return safe(strings.ReplaceAll(text(s), text(sep), "")) },
		"defaultIfEmpty":  defaultIfEmpty,
		"slugify":         slugify,
		"stringFormat":    stringFormat,
		"wordWrap":        wordWrap,
		"yesno":           yesno,
		"stripTags":       func(s any, _ *raymond.Options) any { return safe(tags.ReplaceAllString(text(s), "")) },
		"dateFormat":      dateFormatHelper,
		"dateFormat__1":   dateFormatDefault,
		"isOdd":           func(v any, _ *raymond.Options) any { return parityWord(v, 1, "odd") },
		"isEven":          func(v any, _ *raymond.Options) any { return parityWord(v, 0, "even") },
		"stripes":         stripes,
		"systemValue":     e.systemValue,
		"hostname":        hostname,
	}
}

var tags = regexp.MustCompile(`<[^>]*>`)

// abbreviate implements {{abbreviate text width}}: text longer than width is cut so that it,
// with "...", is width characters long.
func abbreviate(s, width any, _ *raymond.Options) any {
	w, ok := number(width)
	if !ok || w < 4 {
		fail("abbreviate: the width must be a number of at least 4")
	}
	rs := []rune(text(s))
	if len(rs) <= int(w) {
		return safe(string(rs))
	}
	return safe(string(rs[:int(w)-3]) + "...")
}

// capitalizeFirst upper-cases the first character and leaves the rest alone.
func capitalizeFirst(s any, _ *raymond.Options) any {
	in := text(s)
	r, n := utf8.DecodeRuneInString(in)
	if n == 0 {
		return safe("")
	}
	return safe(string(unicode.ToUpper(r)) + in[n:])
}

// capitalize implements {{capitalize text fully=true}}: each word starts with a capital letter, and
// with fully=true the rest of each word is lower case.
func capitalize(s any, o *raymond.Options) any {
	fully := truthy(o.HashProp("fully"))
	var b strings.Builder
	start := true
	for _, r := range text(s) {
		switch {
		case unicode.IsSpace(r):
			start = true
			b.WriteRune(r)
		case start:
			b.WriteRune(unicode.ToUpper(r))
			start = false
		case fully:
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
		}
	}
	return safe(b.String())
}

func leftText(pad string, n int) (string, string)  { return "", strings.Repeat(pad, n) }
func rightText(pad string, n int) (string, string) { return strings.Repeat(pad, n), "" }
func centreText(pad string, n int) (string, string) {
	return strings.Repeat(pad, n/2), strings.Repeat(pad, n-n/2)
}

// justify pads text to size= characters with pad= (a space by default).
func justify(s any, o *raymond.Options, place func(pad string, n int) (before, after string)) any {
	size, ok := number(o.HashProp("size"))
	if !ok {
		fail("the helper needs a size= option")
	}
	pad := " "
	if p := o.HashStr("pad"); p != "" {
		pad = p
	}
	in := text(s)
	n := int(size) - utf8.RuneCountInString(in)
	if n <= 0 {
		return safe(in)
	}
	before, after := place(pad, n)
	return safe(before + in + after)
}

// defaultIfEmpty implements {{defaultIfEmpty value fallback}}; zero, false and an empty list are
// empty as well as no value and "".
func defaultIfEmpty(v, fallback any, _ *raymond.Options) any {
	if v == nil || !truthy(v) || isEmptyList(v) {
		return fallback
	}
	return v
}

func isEmptyList(v any) bool {
	l, ok := plain(v).([]any)
	return ok && len(l) == 0
}

var (
	notSlug    = regexp.MustCompile(`[^\p{L}\p{N}\s]`)
	whitespace = regexp.MustCompile(`\s`)
)

// slugify implements {{slugify text}}: lower case, punctuation removed, each space a hyphen.
func slugify(s any, _ *raymond.Options) any {
	in := strings.ToLower(strings.TrimSpace(text(s)))
	return safe(whitespace.ReplaceAllString(notSlug.ReplaceAllString(in, ""), "-"))
}

// wordWrap implements {{wordWrap text width}}: words are put on lines of at most width characters.
func wordWrap(s, width any, _ *raymond.Options) any {
	w, ok := number(width)
	if !ok || w < 1 {
		fail("wordWrap: the width must be a positive number")
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(text(s)) {
		switch {
		case line == "":
			line = word
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= int(w):
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return safe(strings.Join(lines, "\n"))
}

// yesno implements {{yesno value yes='yes' no='no' maybe='maybe'}}; a missing value is "maybe".
func yesno(v any, o *raymond.Options) any {
	choose := func(name, dflt string) any {
		if s := o.HashStr(name); s != "" {
			return safe(s)
		}
		return safe(dflt)
	}
	switch t := v.(type) {
	case nil:
		return choose("maybe", "maybe")
	case bool:
		if t {
			return choose("yes", "yes")
		}
		return choose("no", "no")
	default:
		fail("yesno: found %q, expected a boolean", text(v))
		return nil
	}
}

// wholeNumber reads a value that is a whole number.
func wholeNumber(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		return int64(t), t == float64(int64(t))
	default:
		return 0, false
	}
}

// parityWord implements isOdd and isEven: the word when the number has the parity, else nothing.
func parityWord(v any, rem int64, word string) any {
	n, ok := wholeNumber(v)
	if ok && (n%2 == rem || n%2 == -rem) {
		return safe(word)
	}
	return safe("")
}

// stripes implements {{stripes index first second}}: first for an even index, second for an odd one.
func stripes(v, first, second any, _ *raymond.Options) any {
	n, ok := wholeNumber(v)
	if !ok {
		fail("stripes: %v is not a whole number", v)
	}
	if n%2 == 0 {
		return first
	}
	return second
}

// systemValue implements {{systemValue type='ENVIRONMENT' key='NAME'}}. WireMock refuses every key
// unless it is permitted, and so does this; only environment variables can be read.
func (e *Engine) systemValue(o *raymond.Options) any {
	key := o.HashStr("key")
	if key == "" {
		return inlineError("The key cannot be empty")
	}
	if !e.permitted[key] {
		return inlineError("Access to %s is denied", key)
	}
	if strings.EqualFold(o.HashStr("type"), "ENVIRONMENT") {
		return safe(os.Getenv(key))
	}
	return safe("")
}

func hostname(_ *raymond.Options) any {
	name, err := os.Hostname()
	if err != nil {
		fail("hostname: %v", err)
	}
	return safe(name)
}

// dateFormatHelper implements {{dateFormat date 'pattern'}}. The pattern is a Java pattern or one of
// full, long, medium and short.
func dateFormatHelper(date, format any, _ *raymond.Options) any {
	t, err := parseTime(text(date), "")
	if err != nil {
		fail("dateFormat: %v", err)
	}
	pattern := text(format)
	switch pattern {
	case "full":
		pattern = "EEEE, MMMM d, yyyy"
	case "long":
		pattern = "MMMM d, yyyy"
	case "medium":
		pattern = "MMM d, yyyy"
	case "short":
		pattern = "M/d/yy"
	}
	return safe(formatTime(t, pattern))
}

// dateFormatDefault is dateFormat with the format given as format=, or medium when it is not.
func dateFormatDefault(date any, o *raymond.Options) any {
	format := o.HashStr("format")
	if format == "" {
		format = "medium"
	}
	return dateFormatHelper(date, format, o)
}

// stringFormat implements {{stringFormat 'Hi %s' name}} with Java's format conversions.
func stringFormat(args ...any) any {
	if len(args) == 0 {
		fail("stringFormat: the format is missing")
	}
	return safe(javaFormat(text(args[0]), args[1:]))
}

var formatSpec = regexp.MustCompile(`%(\d+\$)?([-#+ 0,(]*)(\d+)?(\.\d+)?([a-zA-Z%])`)

// javaFormat applies a java.util.Formatter format. It handles %s %d %x %o %f %e %b %n %% with
// flags, width and precision; other conversions fail as Java's do.
func javaFormat(format string, args []any) string {
	next := 0
	var b strings.Builder
	rest := format
	for {
		loc := formatSpec.FindStringSubmatchIndex(rest)
		if loc == nil {
			if strings.Contains(rest, "%") {
				fail("stringFormat: bad format specifier in %q", format)
			}
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:loc[0]])
		m := formatSpec.FindStringSubmatch(rest)
		rest = rest[loc[1]:]
		conv := m[5]
		switch conv {
		case "%":
			b.WriteString("%")
			continue
		case "n":
			b.WriteString("\n")
			continue
		}
		idx := next
		if m[1] != "" {
			n, err := strconv.Atoi(strings.TrimSuffix(m[1], "$"))
			if err != nil {
				fail("stringFormat: bad argument index in %s", m[0])
			}
			idx = n - 1
		} else {
			next++
		}
		if idx < 0 || idx >= len(args) {
			fail("stringFormat: no argument for format specifier %s", m[0])
		}
		b.WriteString(formatArg(m[2], m[3], m[4], conv, args[idx]))
	}
}

func formatArg(flags, width, precision, conv string, arg any) string {
	goFlags := strings.ReplaceAll(strings.ReplaceAll(flags, ",", ""), "(", "")
	spec := "%" + goFlags + width + precision
	grouped := strings.Contains(flags, ",")
	switch strings.ToLower(conv) {
	case "s":
		out := fmt.Sprintf(spec+"s", javaString(arg))
		if conv == "S" {
			return strings.ToUpper(out)
		}
		return out
	case "b":
		return fmt.Sprintf(spec+"t", truthy(arg))
	case "d", "x", "o":
		n, ok := wholeNumber(arg)
		if !ok {
			fail("stringFormat: %s != %T", conv, arg)
		}
		if grouped && conv == "d" {
			return padTo(width, flags, groupDigitsSigned(n))
		}
		return fmt.Sprintf(spec+conv, n)
	case "f", "e", "g":
		f, ok := number(arg)
		if _, isText := arg.(string); !ok || isText {
			fail("stringFormat: %s != %T", conv, arg)
		}
		return fmt.Sprintf(spec+conv, f)
	default:
		fail("stringFormat: unknown conversion %q", conv)
		return ""
	}
}

func groupDigitsSigned(n int64) string {
	s := new(big.Int).Abs(big.NewInt(n)).String()
	s = groupDigits(s, 3, ",")
	if n < 0 {
		return "-" + s
	}
	return s
}

func padTo(width, flags, s string) string {
	w, err := strconv.Atoi(width)
	if err != nil || len(s) >= w {
		return s
	}
	if strings.Contains(flags, "-") {
		return s + strings.Repeat(" ", w-len(s))
	}
	return strings.Repeat(" ", w-len(s)) + s
}

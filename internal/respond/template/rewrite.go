package template

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/mailgun/raymond/v2"
)

// WireMock templates use forms raymond does not parse: numeric path segments
// (request.query.tag.0, where Handlebars needs .[0]), helpers that assign a variable when
// given a trailing quoted name ({{parseJson body 'order'}}, then order.id), and the block form of
// the matches helper. rewrite turns them into plain Handlebars before parsing.

// argument is one helper argument: a string literal, a subexpression or a path.
const argument = `'[^']*'|"[^"]*"|\([^()]*\)|[^\s'"(){}]+`

var (
	// mustache matches a tag and any run of braces right after it, which is literal text.
	mustache = regexp.MustCompile(`\{\{\{?(?:'[^']*'|"[^"]*"|[^{}'"])*\}\}+`)
	tagCore  = regexp.MustCompile(`^\{\{(?:'[^']*'|"[^"]*"|[^{}'"])*\}\}`)
	raw3Core = regexp.MustCompile(`^\{{3}(?:'[^']*'|"[^"]*"|[^{}'"])*\}{3}`)

	quoted     = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	numericSeg = regexp.MustCompile(`([A-Za-z_\]])\.(\d+)\b`)

	// {{#eq a b}}…{{else}}…{{/eq}} and the other comparison helpers also work as blocks; each becomes
	// an if over the same call.
	condOpen  = regexp.MustCompile(`^\{\{#(eq|neq|gt|gte|lt|lte|and|or|not|contains|matches|isOdd|isEven)\s+(.+?)\s*\}\}$`)
	condClose = regexp.MustCompile(`^\{\{/(eq|neq|gt|gte|lt|lte|and|or|not|contains|matches|isOdd|isEven)\}\}$`)

	// Helpers that take the text between their tags as input are separate helpers, because raymond
	// fixes a helper's number of parameters: {{#assign 'x'}}…{{/assign}} is assignBlock.
	blockOpen  = regexp.MustCompile(`^\{\{#(assign|parseJson|formatJson|formatXml|trim|jsonArrayAdd|jsonMerge)((?:\s.*)?)\}\}$`)
	blockClose = regexp.MustCompile(`^\{\{/(assign|parseJson|formatJson|formatXml|trim|jsonArrayAdd|jsonMerge)\}\}$`)

	// {{formData body 'name' urlDecode=true}} assigns the fields of a url-encoded form.
	assignForm = regexp.MustCompile(`^\{\{\s*formData\s+(` + argument + `)\s+'(\w+)'(?:\s+urlDecode=(true|false))?\s*\}\}$`)

	assignParse = regexp.MustCompile(`^\{\{\s*parseJson\s+(` + argument + `)\s+'(\w+)'\s*\}\}$`)
	assignRegex = regexp.MustCompile(`^\{\{\s*regexExtract\s+(` + argument + `)\s+(` + argument + `)\s+'(\w+)'\s*\}\}$`)
)

// rewrite returns src in plain Handlebars.
func rewrite(src string, known map[string]bool) string {
	return mustache.ReplaceAllStringFunc(src, func(m string) string {
		// {{x}}} is the tag {{x}} followed by a literal brace, as in {"a": {{x}}}. raymond reads three
		// braces as the end of an unescaped tag and fails, so each literal brace becomes a tag of its own.
		core := tagCore
		if strings.HasPrefix(m, "{{{") {
			core = raw3Core
		}
		tail := ""
		if c := core.FindString(m); c != "" {
			tail = strings.Repeat("{{closeBrace}}", len(m)-len(c))
			m = c
		}
		switch {
		case condOpen.MatchString(m):
			return condOpen.ReplaceAllString(m, "{{#if ($1 $2)}}") + tail
		case condClose.MatchString(m):
			return "{{/if}}" + tail
		case blockOpen.MatchString(m):
			return blockForm(m) + tail
		case blockClose.MatchString(m):
			return blockClose.ReplaceAllString(m, "{{/${1}Block}}") + tail
		case assignForm.MatchString(m):
			decode := "false"
			if sub := assignForm.FindStringSubmatch(m); sub[3] == "true" {
				decode = "true"
			}
			return assignForm.ReplaceAllString(m, "{{assign '$2' (formFields $1 "+decode+")}}") + tail
		case assignParse.MatchString(m):
			return assignParse.ReplaceAllString(m, "{{assign '$2' (parseJson $1)}}") + tail
		case assignRegex.MatchString(m):
			return assignRegex.ReplaceAllString(m, "{{assign '$3' (regexGroups $1 $2)}}") + tail
		}
		return wrapPrint(adaptTag(mathFloats(indexSegments(m))), known) + tail
	})
}

// blockForm renames an opening block tag to its Block helper. A parseJson block with no name gets an
// empty one, so that helper always has the same number of parameters.
func blockForm(tag string) string {
	sub := blockOpen.FindStringSubmatch(tag)
	name, args := sub[1], strings.TrimSpace(sub[2])
	if name == "parseJson" && !strings.HasPrefix(args, "'") && !strings.HasPrefix(args, "\"") {
		args = "'' " + args
	}
	return "{{#" + name + "Block " + strings.TrimSpace(indexSegments(args)) + "}}"
}

// indexSegments turns .0 into .[0] outside string literals.
func indexSegments(m string) string {
	var b strings.Builder
	last := 0
	for _, loc := range quoted.FindAllStringIndex(m, -1) {
		b.WriteString(numericSeg.ReplaceAllString(m[last:loc[0]], "$1.[$2]"))
		b.WriteString(m[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(numericSeg.ReplaceAllString(m[last:], "$1.[$2]"))
	return b.String()
}

// inlineDef matches {{#*inline "name"}}…{{/inline}}, Handlebars' way of defining a partial in the
// template that uses it. WireMock's engine supports it and raymond does not, so the definitions
// are taken out of the source and registered as ordinary partials.
var inlineDef = regexp.MustCompile(`(?s)\{\{~?#\*inline\s+(?:"([^"]+)"|'([^']+)')\s*~?\}\}(.*?)\{\{~?/inline~?\}\}`)

type partial struct{ name, body string }

// extractInline returns src without its inline partial definitions, and the definitions in the
// order they were written.
func extractInline(src string) (string, []partial) {
	var found []partial
	main := inlineDef.ReplaceAllStringFunc(src, func(m string) string {
		sub := inlineDef.FindStringSubmatch(m)
		name := sub[1]
		if name == "" {
			name = sub[2]
		}
		found = append(found, partial{name: name, body: sub[3]})
		return ""
	})
	return main, found
}

var (
	mathCall   = regexp.MustCompile(`(?:\{\{\s*|\()math\s`)
	decimalLit = regexp.MustCompile(`-?\d+\.\d+`)
)

// mathFloats quotes the decimal numbers written in a math call. raymond reads 1.0 as the whole
// number 1, and math needs to know the number has a fraction: WireMock's 1.0 / 4 is 0.3 and its
// 1 / 4 is 0. The quoted number is written as Java would print the double, so 2.50 becomes 2.5.
func mathFloats(m string) string {
	if !mathCall.MatchString(m) {
		return m
	}
	var b strings.Builder
	last := 0
	for _, loc := range decimalLit.FindAllStringIndex(m, -1) {
		if !numberBoundary(m, loc[0]-1) || !numberBoundary(m, loc[1]) || insideQuotes(m, loc[0]) {
			continue
		}
		f, err := strconv.ParseFloat(m[loc[0]:loc[1]], 64)
		if err != nil {
			continue
		}
		lit := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(lit, ".") {
			lit += ".0"
		}
		b.WriteString(m[last:loc[0]] + "'" + lit + "'")
		last = loc[1]
	}
	b.WriteString(m[last:])
	return b.String()
}

// numberBoundary reports whether the character at i (or the start or end of the tag) separates a
// number from its neighbours.
func numberBoundary(m string, i int) bool {
	if i < 0 || i >= len(m) {
		return true
	}
	return strings.ContainsRune(" (){}", rune(m[i]))
}

func insideQuotes(m string, at int) bool {
	for _, loc := range quoted.FindAllStringIndex(m, -1) {
		if at >= loc[0] && at < loc[1] {
			return true
		}
	}
	return false
}

var (
	outputTag = regexp.MustCompile(`^\{\{(~?)\s*([^#/>!^&*{}~\s'"][^{}]*?)\s*(~?)\}\}$`)
	bracketed = regexp.MustCompile(`\[[^\]]*\]|'[^']*'|"[^"]*"`)
)

// printValue is what every plain output tag passes its value through. raymond prints a list by
// printing each item in turn, ignoring its String method, so a list is turned into its text here:
// [1, 2] for a list from array, and the first value for a request value that occurs more than once.
func printValue(v any, _ *raymond.Options) any {
	if _, ok := v.(fmt.Stringer); ok && reflect.TypeOf(v).Kind() == reflect.Slice {
		return safe(text(v))
	}
	return v
}

// wrapPrint passes the value of an output tag such as {{request.query.tag}} or {{jsonPath a b}} through
// printValue. Tags that are blocks, partials, comments or the else keyword are left as they are.
func wrapPrint(m string, known map[string]bool) string {
	sub := outputTag.FindStringSubmatch(m)
	if sub == nil {
		return m
	}
	expr := sub[2]
	head, _, _ := strings.Cut(expr, " ")
	switch head {
	case "else", "printValue", "closeBrace":
		return m
	}
	if strings.ContainsAny(bracketed.ReplaceAllString(expr, ""), " \t\n\r") || known[expr] {
		expr = "(" + expr + ")"
	}
	return "{{" + sub[1] + "printValue " + expr + sub[3] + "}}"
}

// A few helpers take optional parameters or options, and raymond fixes the number of parameters a
// helper has, so the rewrite step picks the variant by how many parameters the call has, as
// {{numberFormat 5 'integer'}} to numberFormat__2. WireMock ignores parameters a helper does not
// use, so those are dropped: {{yesno flag 'Y' 'N'}} is {{yesno flag}}.
type arity struct {
	max      int            // parameters beyond this are dropped
	variants map[int]string // the helper to call for a number of parameters
}

func arities() map[string]arity {
	return map[string]arity{
		"numberFormat": {3, map[int]string{0: "numberFormat__0", 1: "numberFormat__1", 2: "numberFormat__2", 3: "numberFormat__3"}},
		"dateFormat":   {2, map[int]string{1: "dateFormat__1"}},
		"yesno":        {1, nil},
		"arrayRemove":  {1, nil},
	}
}

var hashPair = regexp.MustCompile(`^[A-Za-z_]\w*=`)

// adaptTag applies arities to the helper calls in one tag.
func adaptTag(tag string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(tag, "{{"), "}}")
	lead := inner[:len(inner)-len(strings.TrimLeft(inner, "~#"))]
	if strings.HasPrefix(inner, "{") || strings.HasPrefix(strings.TrimLeft(inner, "~"), "/") {
		return tag
	}
	body := inner[len(lead):]
	trail := body[len(strings.TrimRight(body, "~")):]
	body = strings.TrimSuffix(body, trail)
	return "{{" + lead + adaptCall(body) + trail + "}}"
}

// adaptCall rewrites one call, such as "numberFormat 5 'integer'", and the calls nested in it.
func adaptCall(call string) string {
	tokens := splitTop(call)
	for i, t := range tokens {
		if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
			tokens[i] = "(" + adaptCall(t[1:len(t)-1]) + ")"
		}
	}
	if len(tokens) == 0 {
		return call
	}
	a, ok := arities()[tokens[0]]
	if !ok {
		return strings.Join(tokens, " ")
	}
	var params, hash []string
	for _, t := range tokens[1:] {
		if hashPair.MatchString(t) {
			hash = append(hash, t)
		} else {
			params = append(params, t)
		}
	}
	params = params[:min(len(params), a.max)]
	head := tokens[0]
	if v, ok := a.variants[len(params)]; ok {
		head = v
	}
	return strings.Join(append(append([]string{head}, params...), hash...), " ")
}

// splitTop splits a call at the white space that is outside quotes, brackets and parentheses.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, -1
	var quote byte
	flush := func(end int) {
		if start >= 0 {
			out = append(out, s[start:end])
			start = -1
		}
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case (c == ' ' || c == '\t' || c == '\n' || c == '\r') && depth == 0:
			flush(i)
			continue
		}
		if start < 0 {
			start = i
		}
	}
	flush(len(s))
	return out
}

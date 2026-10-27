package template

import (
	"regexp"
	"strings"
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

	// {{#matches text regex}}…{{/matches}} is a block form of the matches helper; it becomes an if.
	matchesOpen = regexp.MustCompile(`^\{\{#matches\s+(.+?)\s*\}\}$`)

	// {{formData body 'name' urlDecode=true}} assigns the fields of a url-encoded form.
	assignForm = regexp.MustCompile(`^\{\{\s*formData\s+(` + argument + `)\s+'(\w+)'(?:\s+urlDecode=(true|false))?\s*\}\}$`)

	assignParse = regexp.MustCompile(`^\{\{\s*parseJson\s+(` + argument + `)\s+'(\w+)'\s*\}\}$`)
	assignRegex = regexp.MustCompile(`^\{\{\s*regexExtract\s+(` + argument + `)\s+(` + argument + `)\s+'(\w+)'\s*\}\}$`)
)

// rewrite returns src in plain Handlebars, and whether it assigns variables.
func rewrite(src string) (string, bool) {
	assigns := false
	out := mustache.ReplaceAllStringFunc(src, func(m string) string {
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
		case matchesOpen.MatchString(m):
			return matchesOpen.ReplaceAllString(m, "{{#if (matches $1)}}") + tail
		case m == "{{/matches}}":
			return "{{/if}}" + tail
		case assignForm.MatchString(m):
			assigns = true
			decode := "false"
			if sub := assignForm.FindStringSubmatch(m); sub[3] == "true" {
				decode = "true"
			}
			return assignForm.ReplaceAllString(m, "{{assign '$2' (formFields $1 "+decode+")}}") + tail
		case assignParse.MatchString(m):
			assigns = true
			return assignParse.ReplaceAllString(m, "{{assign '$2' (parseJson $1)}}") + tail
		case assignRegex.MatchString(m):
			assigns = true
			return assignRegex.ReplaceAllString(m, "{{assign '$3' (regexGroups $1 $2)}}") + tail
		}
		return indexSegments(m) + tail
	})
	return out, assigns
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

package template

import (
	"regexp"
	"strings"
)

// WireMock templates use two forms raymond does not parse: numeric path segments
// (request.query.tag.0, where Handlebars needs .[0]) and helpers that assign a variable when
// given a trailing quoted name ({{parseJson body 'order'}}, then order.id). rewrite turns
// both into plain Handlebars before parsing.

// argument is one helper argument: a string literal, a subexpression or a path.
const argument = `'[^']*'|"[^"]*"|\([^()]*\)|[^\s'"(){}]+`

var (
	mustache   = regexp.MustCompile(`\{\{\{?(?:'[^']*'|"[^"]*"|[^{}'"])*\}?\}\}`)
	quoted     = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	numericSeg = regexp.MustCompile(`([A-Za-z_\]])\.(\d+)\b`)

	assignParse = regexp.MustCompile(`^\{\{\s*parseJson\s+(` + argument + `)\s+'(\w+)'\s*\}\}$`)
	assignRegex = regexp.MustCompile(`^\{\{\s*regexExtract\s+(` + argument + `)\s+(` + argument + `)\s+'(\w+)'\s*\}\}$`)
)

// rewrite returns src in plain Handlebars, and whether it assigns variables.
func rewrite(src string) (string, bool) {
	assigns := false
	out := mustache.ReplaceAllStringFunc(src, func(m string) string {
		switch {
		case assignParse.MatchString(m):
			assigns = true
			return assignParse.ReplaceAllString(m, "{{assign '$2' (parseJson $1)}}")
		case assignRegex.MatchString(m):
			assigns = true
			return assignRegex.ReplaceAllString(m, "{{assign '$3' (regexGroups $1 $2)}}")
		}
		return indexSegments(m)
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

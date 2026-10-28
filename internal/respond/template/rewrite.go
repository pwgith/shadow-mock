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

	// {{#eq a b}}…{{else}}…{{/eq}} and the other comparison helpers also work as blocks; each becomes
	// an if over the same call.
	condOpen  = regexp.MustCompile(`^\{\{#(eq|neq|gt|gte|lt|lte|and|or|not|contains|matches)\s+(.+?)\s*\}\}$`)
	condClose = regexp.MustCompile(`^\{\{/(eq|neq|gt|gte|lt|lte|and|or|not|contains|matches)\}\}$`)

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
func rewrite(src string) string {
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
		return indexSegments(m) + tail
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

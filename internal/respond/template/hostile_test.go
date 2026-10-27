package template_test

import (
	"fmt"
	"strings"
	"testing"
)

// Request data is written by whoever can reach the mock port, and webhook templates render in
// a background goroutine, where a panic would stop the whole server. So no helper may let a
// panic escape Render, whatever it is given.
func TestNoHelperPanicsOnHostileInput(t *testing.T) {
	t.Parallel()
	helpers := []string{
		"now", "formatDate", "parseDate", "truncateDate", "randomValue", "randomInt",
		"base64", "urlEncode", "regexExtract", "trim", "lower", "upper", "capitalize", "replace",
		"substring", "join", "size", "jsonPath", "xPath", "parseJson", "toJson", "formatJson",
		"jsonMerge", "jsonRemove", "jsonArrayAdd", "array", "math", "eq", "neq", "gt", "gte",
		"lt", "lte", "contains", "and", "or", "not",
	}
	values := []string{
		"", "'x'", "''", "0", "-1", "99999999999999999999", "1.5e308", "true", "nil", "missing",
		"name", "items", "body", "xml", "flag",
		"'[1,2'", "'{\"a\":'", "'$..'", "'$[?(@.x'", "'(('", "'<a'", "'//['",
		"(array 1 2)", "(array)", "'0'", "'-5'", "'+'", "'/'", "'%'",
	}
	hashes := []string{"", " length=-1", " length=99999999", " lower=5 upper=1", " lower=-9 upper=-9", " format='%%%'", " offset='x y z'", " timezone='Mars/Olympus'", " type='NOPE'"}

	e := engine()
	run := func(src string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic escaped Render for %s: %v", src, r)
			}
		}()
		_, _ = e.Render(src, data())
	}
	for _, h := range helpers {
		for _, hash := range hashes {
			run("{{" + h + hash + "}}")
		}
		for _, a := range values {
			run(fmt.Sprintf("{{%s %s}}", h, a))
			for _, b := range values {
				run(fmt.Sprintf("{{%s %s %s}}", h, a, b))
			}
		}
	}
	// Three arguments, with the middle one varied, covers math, replace, substring and truncateDate.
	for _, h := range []string{"math", "replace", "substring", "truncateDate", "regexExtract", "jsonMerge", "jsonArrayAdd", "jsonRemove", "formatDate", "parseDate"} {
		for _, a := range values {
			for _, b := range values {
				for _, c := range []string{"'x'", "0", "-1", "'/'", "99999999999"} {
					run(fmt.Sprintf("{{%s %s %s %s}}", h, a, b, c))
				}
			}
		}
	}
}

// Pathological templates must fail or finish, not run for ever or exhaust memory.
func TestTemplateSizeIsBounded(t *testing.T) {
	t.Parallel()
	e := engine()
	big := strings.Repeat("{{randomValue length=10000}}", 200)
	out, err := e.Render(big, data())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(out) > 5_000_000 {
		t.Errorf("output of %d bytes for a 200-helper template", len(out))
	}
	if _, err := e.Render(strings.Repeat("{{#if true}}", 5000)+strings.Repeat("{{/if}}", 5000), data()); err != nil {
		t.Logf("deeply nested template: %v", err)
	}
}

// Templates come from mapping authors but reach request data, so rendering must return an error
// for anything it cannot handle and never panic.
func FuzzRender(f *testing.F) {
	for _, s := range []string{
		"{{request.path}}", "{{#each items}}{{sku}}{{/each}}", "{{jsonPath body '$.customer.name'}}",
		"{{math 1 '/' 0}}", "{{substring name 5 1}}", "{{formatDate name format='yyyy'}}", "{{parseJson body 'x'}}{{x.n}}",
		"{{regexExtract name '(' 'm'}}", "{{#if (eq 1 2)}}a{{else}}b{{/if}}", "{{xPath xml '//id/text()'}}",
		"{{now offset='1 day' timezone='UTC'}}", "{{randomValue length=3}}", "{{{{raw}}}}", "{{> partial}}", "{{",
	} {
		f.Add(s)
	}
	e := engine()
	f.Fuzz(func(_ *testing.T, src string) {
		_, _ = e.Render(src, data())
	})
}

// A body at the size limit can be a million levels deep, and walking it would overflow the stack.
func TestXPathHelperRefusesDeeplyNestedXML(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("<a>", 1_000_000) + "x" + strings.Repeat("</a>", 1_000_000)
	d := data()
	d["deep"] = deep
	if _, err := engine().Render("{{xPath deep '//a'}}", d); err == nil {
		t.Error("a document a million levels deep should be refused")
	}
}

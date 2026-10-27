package template_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/respond/template"
)

var now = time.Date(2026, time.November, 1, 9, 30, 15, 0, time.UTC)

func engine() *template.Engine { return template.New(clock.NewFake(now)) }

func data() map[string]any {
	return template.Safe(map[string]any{
		"name":  "Ada & <Bob>",
		"items": []any{map[string]any{"sku": "a", "qty": 2.0, "price": 1.5}, map[string]any{"sku": "b", "qty": 1.0, "price": 4.0}},
		"body":  `{"customer":{"name":"Ada"},"n":15,"list":[1,2]}`,
		"xml":   `<claim><id>77</id><id>78</id></claim>`,
		"flag":  true,
	}).(map[string]any)
}

func TestRender(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, tpl, want string }{
		{"text without mustaches", "plain {text}", "plain {text}"},
		{"a value is not HTML-escaped", "{{name}}", "Ada & <Bob>"},
		{"triple stash", "{{{name}}}", "Ada & <Bob>"},
		{"if and else", "{{#if flag}}yes{{else}}no{{/if}}", "yes"},
		{"each with block params", "{{#each items as |i|}}{{i.sku}}{{#unless @last}},{{/unless}}{{/each}}", "a,b"},
		{"jsonPath string", "{{jsonPath body '$.customer.name'}}", "Ada"},
		{"jsonPath number", "{{jsonPath body '$.n'}}", "15"},
		{"jsonPath list", "{{toJson (jsonPath body '$.list')}}", "[1,2]"},
		{"jsonPath list iterates", "{{#each (jsonPath body '$.list')}}<{{this}}>{{/each}}", "<1><2>"},
		{"jsonPath object prints as JSON", "{{jsonPath body '$.customer'}}", `{"name":"Ada"}`},
		{"jsonPath no match", "[{{jsonPath body '$.nope'}}]", "[]"},
		{"xPath", "{{xPath xml '/claim/id/text()'}}", "77"},
		{"xPath no match", "[{{xPath xml '/claim/none'}}]", "[]"},
		{"parseJson with", "{{#with (parseJson body)}}{{customer.name}}{{/with}}", "Ada"},
		{"parseJson assigns a variable", "{{parseJson body 'b'}}{{b.customer.name}}-{{b.n}}", "Ada-15"},
		{"regexExtract", "{{regexExtract 'ab-123' '[0-9]+'}}", "123"},
		{"regexExtract group", "{{regexExtract 'ab-123' '([a-z]+)-'}}", "ab"},
		{"regexExtract default", "{{regexExtract 'abc' '[0-9]+' default='none'}}", "none"},
		{"regexExtract assigns groups", "{{regexExtract 'ORD-778' '([A-Z]{3})-([0-9]+)' 'p'}}{{p.0}}/{{p.1}}", "ORD/778"},
		{"math", "{{math 7 '%' 4}} {{math 3 '*' 4}} {{math 1 '/' 4}} {{math 5 '-' 7}} {{math 2 '+' 2}}", "3 12 0.25 -2 4"},
		{"math on items", "{{#each items}}{{math qty '*' price}};{{/each}}", "3;4;"},
		{"base64", "{{base64 'hello'}}", "aGVsbG8="},
		{"base64 decode", "{{base64 'aGVsbG8=' decode=true}}", "hello"},
		{"base64 unpadded", "{{base64 'a' padding=false}}", "YQ"},
		{"urlEncode", "{{urlEncode 'a b&c'}}", "a+b%26c"},
		{"string helpers", "{{upper 'ab'}}{{lower 'CD'}}{{capitalize 'ef'}}{{trim '  g '}}{{replace 'aXa' 'X' '-'}}", "ABcdEfga-a"},
		{"substring", "{{substring 'abcdef' 1 3}}", "bc"},
		{"size and join", "{{size items}} {{join (array 'a' 'b' 'c') ','}}", "2 a,b,c"},
		{"array in each", "{{#each (array 1 2 3)}}{{this}}{{/each}}", "123"},
		{"comparison", "{{#if (eq 2 2)}}a{{/if}}{{#if (gt 3 2)}}b{{/if}}{{#if (lt 3 2)}}c{{/if}}{{#if (neq 'x' 'y')}}d{{/if}}", "abd"},
		{"logic", "{{#if (and flag (not false))}}a{{/if}}{{#if (or false '')}}b{{/if}}", "a"},
		{"contains", "{{#if (contains 'hello' 'ell')}}y{{/if}}", "y"},
		{"toJson", "{{toJson (parseJson body)}}", `{"customer":{"name":"Ada"},"list":[1,2],"n":15}`},
		{"formatJson compact", "{{formatJson body format='compact'}}", `{"customer":{"name":"Ada"},"list":[1,2],"n":15}`},
		{"formatJson pretty", "{{formatJson '{\"a\":1}'}}", "{\n  \"a\": 1\n}"},
		{"jsonMerge", `{{jsonMerge '{"a":{"x":1},"b":2}' '{"a":{"y":3},"b":null}' removeNulls=true}}`, `{"a":{"x":1,"y":3}}`},
		{"jsonRemove", `{{jsonRemove '[{"id":1},{"id":2}]' '$.[?(@.id == 1)]'}}`, `[{"id":2}]`},
		{"jsonArrayAdd", `{{jsonArrayAdd '[1,2,3]' '4' maxItems=3}}`, `[2,3,4]`},
		{"numeric segments need no brackets", "{{items.0.sku}}{{items.1.sku}}", "ab"},
		{"a dot in a string is left alone", "{{regexExtract 'a.1' 'a.1'}}", "a.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := engine().Render(tt.tpl, data())
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.tpl, got, tt.want)
			}
		})
	}
}

func TestRenderTime(t *testing.T) {
	t.Parallel()

	e := template.New(clock.NewFake(now))
	tests := []struct{ tpl, want string }{
		{"{{now}}", "2026-11-01T09:30:15Z"},
		{"{{now format='yyyy-MM-dd'}}", "2026-11-01"},
		{"{{now offset='2 days' format='yyyy-MM-dd'}}", "2026-11-03"},
		{"{{now offset='-3 hours' format='HH:mm:ss'}}", "06:30:15"},
		{"{{now offset='1 months' format='MMM d, yyyy'}}", "Dec 1, 2026"},
		{"{{now format='epoch'}}", "1793525415000"},
		{"{{now format='unix'}}", "1793525415"},
		{"{{now timezone='Asia/Tokyo' format=\"yyyy-MM-dd'T'HH:mm\"}}", "2026-11-01T18:30"},
		{"{{formatDate '2026-01-02T03:04:05Z' format='dd/MM/yy'}}", "02/01/26"},
		{"{{parseDate '02/01/2026' format='dd/MM/yyyy'}}", "2026-01-02T00:00:00Z"},
		{"{{truncateDate '2026-11-17T09:30:00Z' 'first day of month' format='yyyy-MM-dd'}}", "2026-11-01"},
		{"{{truncateDate '2026-11-17T09:30:00Z' 'last day of month' format='yyyy-MM-dd'}}", "2026-11-30"},
	}
	for _, tt := range tests {
		got, err := e.Render(tt.tpl, nil)
		if err != nil {
			t.Errorf("Render(%q): %v", tt.tpl, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Render(%q) = %q, want %q", tt.tpl, got, tt.want)
		}
	}
}

func TestRenderRandom(t *testing.T) {
	t.Parallel()
	tests := []struct{ tpl, pattern string }{
		{"{{randomValue length=8 type='ALPHANUMERIC'}}", `^[A-Za-z0-9]{8}$`},
		{"{{randomValue length=5 type='ALPHABETIC' uppercase=true}}", `^[A-Z]{5}$`},
		{"{{randomValue length=4 type='NUMERIC'}}", `^[0-9]{4}$`},
		{"{{randomValue length=6 type='HEXADECIMAL'}}", `^[0-9a-f]{6}$`},
		{"{{randomValue type='UUID'}}", `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`},
		{"{{randomValue}}", `^[A-Za-z0-9]{36}$`},
		{"{{randomInt lower=5 upper=7}}", `^[567]$`},
	}
	for _, tt := range tests {
		got, err := engine().Render(tt.tpl, nil)
		if err != nil {
			t.Errorf("Render(%q): %v", tt.tpl, err)
			continue
		}
		if !regexp.MustCompile(tt.pattern).MatchString(got) {
			t.Errorf("Render(%q) = %q, want a match for %s", tt.tpl, got, tt.pattern)
		}
	}
}

func TestRenderErrors(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, tpl, want string }{
		{"unclosed block", "{{#if x}}", "parse template"},
		{"bad math operator", "{{math 1 '^' 2}}", "unknown operator"},
		{"division by zero", "{{math 1 '/' 0}}", "division by zero"},
		{"math on text", "{{math 'a' '+' 1}}", "must be numbers"},
		{"bad JSON", "{{jsonPath 'nope' '$.a'}}", "not valid JSON"},
		{"bad JSONPath", "{{jsonPath body '$[['}}", "JSONPath"},
		{"bad XML", "{{xPath '<a>' '/a'}}", "not valid XML"},
		{"bad regex", "{{regexExtract 'a' '('}}", "regexExtract"},
		{"bad random type", "{{randomValue type='NOPE'}}", "unknown type"},
		{"bad length", "{{randomValue length=99999}}", "invalid length"},
		{"bad offset", "{{now offset='soon'}}", "offset"},
		{"bad time zone", "{{now timezone='Nowhere/Land'}}", "time zone"},
		{"bad date", "{{formatDate 'yesterday'}}", "parse date"},
		{"array add to a non-array", `{{jsonArrayAdd '{"a":1}' '1'}}`, "JSON array"},
	}
	for _, tt := range tests {
		_, err := engine().Render(tt.tpl, data())
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Render(%q) error = %v, want one mentioning %q", tt.name, tt.tpl, err, tt.want)
		}
	}
}

func TestRenderCannotReachOutside(t *testing.T) {
	t.Parallel()
	// There is no file or http helper, so these print nothing and read nothing.
	for _, tpl := range []string{"{{file '/etc/passwd'}}", "{{http 'http://example.com'}}", "{{> secret}}"} {
		got, err := engine().Render(tpl, data())
		if strings.Contains(got, "root:") || strings.Contains(got, "Example") {
			t.Errorf("Render(%q) = %q, which leaks outside content", tpl, got)
		}
		_ = err
	}
}

func TestRenderIsRepeatableAndShared(t *testing.T) {
	t.Parallel()
	e := engine()
	for range 3 { // the second and third renders use the cached template, including its variables
		got, err := e.Render("{{parseJson body 'b'}}{{b.n}}", data())
		if err != nil || got != "15" {
			t.Fatalf("Render = %q, %v", got, err)
		}
	}
}

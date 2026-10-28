package jsonpath_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/pwgith/shadow-mock/internal/jsonpath"
)

// goldenCase is one expression and what a real WireMock 3.13.2 answered for it: whether the
// body matched, or "rejected" when it refused the mapping. Regenerate the file with
// docs/PARITY_PLAN.md's probe (jp-matrix.mjs) when WireMock is upgraded.
type goldenCase struct {
	Expr    string `json:"expr"`
	Doc     any    `json:"doc"`
	Matched any    `json:"matched"`
}

// knownDifferences are expressions where WireMock's answer comes from an accident of its parser,
// not from the language. Shadow Mock answers differently on purpose, and says so in COMPATIBILITY.md.
var knownDifferences = map[string]string{
	"$.e[": "WireMock reads an unclosed bracket as if it were closed; Shadow Mock rejects the expression",
}

func TestMatchesAgreeWithRealWireMock(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/jayway_wiremock_3_13_2.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 150 {
		t.Fatalf("only %d golden cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Expr, func(t *testing.T) {
			t.Parallel()
			p, err := jsonpath.Parse(c.Expr)
			if c.Matched == "rejected" {
				if err == nil {
					t.Errorf("WireMock rejects %q but it parsed", c.Expr)
				}
				return
			}
			if err != nil {
				if reason, ok := knownDifferences[c.Expr]; ok {
					t.Skipf("known difference: %s (%v)", reason, err)
				}
				t.Fatalf("WireMock accepts %q but it did not parse: %v", c.Expr, err)
			}
			if got := p.Matches(c.Doc); got != c.Matched {
				if reason, ok := knownDifferences[c.Expr]; ok {
					t.Skipf("known difference: %s", reason)
				}
				t.Errorf("Matches(%s) = %v, WireMock says %v", c.Expr, got, c.Matched)
			}
		})
	}
}

func TestValues(t *testing.T) {
	t.Parallel()
	var doc any
	_ = json.Unmarshal([]byte(`{"store":{"book":[{"title":"A","price":8.5,"tags":["x","y"]},{"title":"B","price":12},{"title":"C","price":3}]},"n":null,"s":"abc"}`), &doc)
	tests := []struct {
		expr string
		want []any
	}{
		{"$.store.book[0].title", []any{"A"}},
		{"$.store.book[*].title", []any{"A", "B", "C"}},
		{"$..title", []any{"A", "B", "C"}},
		{"$.store.book[?(@.price > 5)].title", []any{"A", "B"}},
		{"$.store.book[?(@.price > 5 && @.title != 'A')].title", []any{"B"}},
		{"$.store.book[-1].title", []any{"C"}},
		{"$.store.book[0,2].title", []any{"A", "C"}},
		{"$.store.book[1:].title", []any{"B", "C"}},
		{"$.store.book[:1].title", []any{"A"}},
		{"$.store.book.length()", []any{3.0}},
		{"$.store.book[*].price.size()", []any{3.0}},
		{"$.store.book[0].tags.first()", []any{"x"}},
		{"$.store.book[0].tags.last()", []any{"y"}},
		{"$.store.book[0].tags.index(1)", []any{"y"}},
		{"$.store.book[0].keys()", []any{[]any{"price", "tags", "title"}}},
		{"$.store.book[?(@.tags contains 'x')].title", []any{"A"}},
		{"$.store.book[?(@.title =~ /[AB]/)].title", []any{"A", "B"}},
		{"$.store.book[?(@.title in ['C','Z'])].title", []any{"C"}},
		{"$.store.book[?(@.price < $.store.book[0].price)].title", []any{"C"}},
		{"$.store.book[?(!@.tags)].title", []any{"B", "C"}},
		{"$['store']['book'][0]['title']", []any{"A"}},
		{"$.s.concat('d')", []any{"abcd"}},
		{"$.n", []any{nil}},
		{"$.missing", nil},
		{"title", nil},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			p, err := jsonpath.Parse(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			got := p.Get(doc)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Get = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRejects(t *testing.T) {
	t.Parallel()
	for _, expr := range []string{"", "$a", "$.", "$[", "$..", "$.e[?(@ >)]", "$.e[?(", "$[?(@.a == 'x']", "$[?(true)]", "$.a.nope()", "$.a[?(@.b ~~ 1)]", "$['a", "$[?(@.a =~ /(/)]"} {
		if _, err := jsonpath.Parse(expr); err == nil {
			t.Errorf("Parse(%q) accepted a bad expression", expr)
		}
	}
}

func TestDefinite(t *testing.T) {
	t.Parallel()
	for expr, want := range map[string]bool{"$.a.b": true, "$['a'][0]": true, "$.a[*]": false, "$..a": false, "$.a[?(@.b)]": false, "$.a[0,1]": false, "$.a[1:2]": false, "$.a[-1]": true} {
		p, err := jsonpath.Parse(expr)
		if err != nil {
			t.Fatal(err)
		}
		if p.Definite() != want {
			t.Errorf("Definite(%s) = %v, want %v", expr, p.Definite(), want)
		}
	}
}

func FuzzParseAndGet(f *testing.F) {
	for _, s := range []string{"$.a", "$..a[?(@.b > 1)]", "$[?(@.x =~ /a/i)]", "$.a.size()", "$['a','b']", "$.a[1:2]"} {
		f.Add(s)
	}
	var doc any
	_ = json.Unmarshal([]byte(`{"a":[{"b":2,"x":"A"},{"b":0}],"s":"t"}`), &doc)
	f.Fuzz(func(_ *testing.T, expr string) {
		p, err := jsonpath.Parse(expr)
		if err != nil {
			return
		}
		_ = p.Get(doc)
		_ = p.Matches(doc)
	})
}

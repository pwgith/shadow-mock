package graphql_test

import (
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/graphql"
	"github.com/pwgith/shadow-mock/pkg/extension"
)

func TestQueriesAreEqualApartFromLayoutAndOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		same bool
	}{
		{"whitespace and commas", `{ user(id: 1) { name, email } }`, "{\n  user(id: 1) {\n    name\n    email\n  }\n}", true},
		{"comments", "{ a # one\n b }", "{ a b }", true},
		{"shorthand and query", `{ a }`, `query { a }`, true},
		{"field order", `{ a b c }`, `{ c a b }`, true},
		{"argument order", `{ f(x: 1, y: 2) }`, `{ f(y: 2, x: 1) }`, true},
		{"object value field order", `{ f(o: {a: 1, b: 2}) }`, `{ f(o: {b: 2, a: 1}) }`, true},
		{"variable definition order", `query($a: Int, $b: String) { f }`, `query($b: String, $a: Int) { f }`, true},
		{"directive order", `{ a @x @y }`, `{ a @y @x }`, true},
		{"definition order", `query A { ...F } fragment F on T { x }`, `fragment F on T { x } query A { ...F }`, true},
		{"nested field order", `{ a { c b } }`, `{ a { b c } }`, true},
		{"block string and string", `{ f(s: """hi""") }`, `{ f(s: "hi") }`, true},
		{"escapes", `{ f(s: "aA") }`, `{ f(s: "aA") }`, true},
		{"a different field", `{ a }`, `{ b }`, false},
		{"a different argument value", `{ f(x: 1) }`, `{ f(x: 2) }`, false},
		{"list order matters", `{ f(l: [1, 2]) }`, `{ f(l: [2, 1]) }`, false},
		{"an alias matters", `{ a: x }`, `{ b: x }`, false},
		{"operation name matters", `query A { x }`, `query B { x }`, false},
		{"operation kind matters", `query { x }`, `mutation { x }`, false},
		{"a different type condition", `{ ... on A { x } }`, `{ ... on B { x } }`, false},
		{"a variable and a literal", `{ f(x: $v) }`, `{ f(x: 1) }`, false},
		{"string and enum", `{ f(x: "A") }`, `{ f(x: A) }`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := graphql.Equal(tt.a, tt.b)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.same {
				t.Errorf("Equal = %v, want %v", got, tt.same)
			}
		})
	}
}

func TestInvalidQueriesAreSyntaxErrors(t *testing.T) {
	t.Parallel()
	for name, q := range map[string]string{
		"empty":                "",
		"only a comment":       "# nothing",
		"unclosed selection":   "{ a ",
		"empty selection":      "{ }",
		"no name":              "query { }",
		"stray character":      "{ a } ?",
		"unterminated string":  `{ f(s: "x) }`,
		"a schema definition":  "type A { b: Int }",
		"a bad number":         "{ f(x: 01) }",
		"a number then a name": "{ f(x: 1a) }",
		"fragment named on":    "fragment on on T { x }",
		"a missing value":      "{ f(x: ) }",
		"an unclosed list":     "{ f(x: [1, 2) }",
		"a missing colon":      "{ f(x 1) }",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := graphql.Canonical(q); err == nil {
				t.Errorf("%q should be a syntax error", q)
			}
		})
	}
}

func TestDeepNestingIsRefusedNotCrashed(t *testing.T) {
	t.Parallel()
	q := strings.Repeat("{a", 5000) + strings.Repeat("}", 5000)
	if _, err := graphql.Canonical(q); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Errorf("want a nesting error, got %v", err)
	}
	l := "{ f(x: " + strings.Repeat("[", 5000) + strings.Repeat("]", 5000) + ") }"
	if _, err := graphql.Canonical(l); err == nil {
		t.Error("want an error for lists nested too deep")
	}
}

func FuzzCanonical(f *testing.F) {
	for _, s := range []string{`{ a }`, `query($a: [Int!]! = [1]) @d { f(o: {a: "x"}) { ... on T { b } ...F } }`, `"""`, `{ f(x: 1.5e3) }`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, err := graphql.Canonical(s)
		if err != nil {
			return
		}
		// A canonical form is itself a query in the same class, with the same canonical form.
		if b, err := graphql.Canonical(a); err == nil && a != b {
			t.Errorf("canonical form not stable: %q then %q", a, b)
		}
	})
}

func match(m extension.RequestMatcher, body string, params map[string]any) bool {
	return m.Match(extension.Request{Method: "POST", Body: []byte(body)}, params)
}

func TestTheMatcherChecksQueryVariablesAndOperationName(t *testing.T) {
	t.Parallel()
	m := graphql.Matcher()
	if m.Name() != "graphql-body-matcher" {
		t.Fatalf("name = %q", m.Name())
	}
	query := `query Q($id: ID!) { user(id: $id) { name email } }`
	params := map[string]any{"query": query, "variables": map[string]any{"id": float64(7), "tags": []any{"a", "b"}}, "operationName": "Q"}
	tests := []struct {
		name   string
		body   string
		params map[string]any
		want   bool
	}{
		{"the same request", `{"query":"query Q($id: ID!) { user(id: $id) { name email } }","variables":{"id":7,"tags":["a","b"]},"operationName":"Q"}`, params, true},
		{
			"query laid out differently, fields reordered and variable keys reordered",
			`{"operationName":"Q","variables":{"tags":["a","b"],"id":7},"query":"query Q($id: ID!) {\n user(id: $id) {\n email\n name } }"}`, params, true,
		},
		{"another query", `{"query":"query Q($id: ID!) { user(id: $id) { name } }","variables":{"id":7,"tags":["a","b"]},"operationName":"Q"}`, params, false},
		{"other variables", `{"query":"query Q($id: ID!) { user(id: $id) { name email } }","variables":{"id":8,"tags":["a","b"]},"operationName":"Q"}`, params, false},
		{"an array in another order", `{"query":"query Q($id: ID!) { user(id: $id) { name email } }","variables":{"id":7,"tags":["b","a"]},"operationName":"Q"}`, params, false},
		{"an extra variable", `{"query":"query Q($id: ID!) { user(id: $id) { name email } }","variables":{"id":7,"tags":["a","b"],"x":1},"operationName":"Q"}`, params, false},
		{"missing variables", `{"query":"query Q($id: ID!) { user(id: $id) { name email } }","operationName":"Q"}`, params, false},
		{"another operation name", `{"query":"query Q($id: ID!) { user(id: $id) { name email } }","variables":{"id":7,"tags":["a","b"]},"operationName":"Z"}`, params, false},
		{"no operation name", `{"query":"query Q($id: ID!) { user(id: $id) { name email } }","variables":{"id":7,"tags":["a","b"]}}`, params, false},
		{"no query", `{"variables":{"id":7}}`, params, false},
		{"not JSON", `query { x }`, params, false},
		{"an invalid query", `{"query":"{ user("}`, params, false},
		{"no variables expected and none sent", `{"query":"{ a }"}`, map[string]any{"query": "{ a }"}, true},
		{"no variables expected and null sent", `{"query":"{ a }","variables":null}`, map[string]any{"query": "{ a }"}, true},
		{"no variables expected but some sent", `{"query":"{ a }","variables":{"x":1}}`, map[string]any{"query": "{ a }"}, false},
		{"no operation name expected but one sent", `{"query":"{ a }","operationName":"X"}`, map[string]any{"query": "{ a }"}, false},
		{"the mapping has no query", `{"query":"{ a }"}`, map[string]any{}, false},
		{"the mapping's query is invalid", `{"query":"{ a }"}`, map[string]any{"query": "{ a"}, false},
	}
	for _, tt := range tests {
		if got := match(m, tt.body, tt.params); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

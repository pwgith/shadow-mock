package jsonpath_test

import (
	"encoding/json"
	"testing"

	"github.com/pwgith/shadow-mock/internal/jsonpath"
)

// TestFilterComparisons covers how a filter compares lists, objects, numbers and strings, which is where Jayway
// differs from the standard: a number equals a string that reads as it, and objects and lists compare as wholes.
func TestFilterComparisons(t *testing.T) {
	t.Parallel()
	var doc any
	if err := json.Unmarshal([]byte(`{"items":[
		{"id":1,"code":"7","tags":["a","b"],"meta":{"k":1,"j":"2"}},
		{"id":2,"code":"x","tags":["a"],"meta":{"k":1}},
		{"id":3,"code":"9","tags":["a","b"],"meta":{"k":2,"j":"2"}}
	]}`), &doc); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		expr string
		want []float64
	}{
		{"a number equals a string that reads as it", "$.items[?(@.code == 7)].id", []float64{1}},
		{"a string equals a number that reads as it", "$.items[?(@.id == '2')].id", []float64{2}},
		{"a number does not equal text", "$.items[?(@.code == 0)].id", nil},
		{"lists are equal element by element", `$.items[?(@.tags == ['a','b'])].id`, []float64{1, 3}},
		{"lists of different length differ", `$.items[?(@.tags == ['a','b','c'])].id`, nil},
		{"lists with a different element differ", `$.items[?(@.tags == ['a','z'])].id`, nil},
		{"objects are equal key by key", "$.items[?(@.meta == $.items[0].meta)].id", []float64{1}},
		{"objects missing a key differ", "$.items[?(@.meta == $.items[1].meta)].id", []float64{2}},
		{"objects with another value differ", "$.items[?(@.meta == $.items[2].meta)].id", []float64{3}},
		{"a list is not an object", "$.items[?(@.tags == $.items[0].meta)].id", nil},
		{"numbers order", "$.items[?(@.id >= 2)].id", []float64{2, 3}},
		{"strings order", "$.items[?(@.code > '7')].id", []float64{2, 3}},
		{"a number does not order against a string", "$.items[?(@.id > 'a')].id", nil},
		{"a string does not order against a number", "$.items[?(@.code > 1)].id", nil},
		{"a list does not order", "$.items[?(@.tags > 1)].id", nil},
		{"less than or equal", "$.items[?(@.id <= 1)].id", []float64{1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := jsonpath.Parse(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			var got []float64
			for _, v := range p.Get(doc) {
				got = append(got, v.(float64))
			}
			if len(got) != len(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
				}
			}
		})
	}
}

func TestStringReturnsTheExpression(t *testing.T) {
	t.Parallel()
	p, err := jsonpath.Parse("$.a.b")
	if err != nil {
		t.Fatal(err)
	}
	if p.String() != "$.a.b" {
		t.Errorf("String = %q, want the expression", p.String())
	}
}

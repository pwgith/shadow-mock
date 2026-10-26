package match

import (
	"errors"
	"testing"
)

func TestCompileContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		body    string
		want    bool
	}{
		{"string matcher on body", `{"contains":"order"}`, `{"order":1}`, true},
		{"absent body", `{"absent":true}`, ``, true},
		{"equalToJson exact", `{"equalToJson":{"a":1,"b":[1,2]}}`, `{"b":[1,2],"a":1}`, true},
		{"equalToJson as string", `{"equalToJson":"{\"a\":1}"}`, `{"a":1.0}`, true},
		{"equalToJson extra field", `{"equalToJson":{"a":1}}`, `{"a":1,"b":2}`, false},
		{"equalToJson ignoreExtraElements", `{"equalToJson":{"a":1},"ignoreExtraElements":true}`, `{"a":1,"b":2}`, true},
		{"equalToJson array order", `{"equalToJson":[1,2]}`, `[2,1]`, false},
		{"equalToJson ignoreArrayOrder", `{"equalToJson":[1,2],"ignoreArrayOrder":true}`, `[2,1]`, true},
		{"equalToJson ignoreArrayOrder missing", `{"equalToJson":[1,3],"ignoreArrayOrder":true}`, `[2,1]`, false},
		{"equalToJson array extra elements", `{"equalToJson":[1],"ignoreExtraElements":true}`, `[1,2]`, true},
		{"equalToJson array length", `{"equalToJson":[1]}`, `[1,2]`, false},
		{"equalToJson missing key", `{"equalToJson":{"a":1,"b":2}}`, `{"a":1}`, false},
		{"equalToJson wrong type", `{"equalToJson":{"a":{"b":1}}}`, `{"a":[1]}`, false},
		{"equalToJson not json", `{"equalToJson":{"a":1}}`, `nope`, false},
		{"placeholder ignore", `{"equalToJson":{"id":"${json-unit.ignore}","a":1}}`, `{"id":"x","a":1}`, true},
		{"placeholder ignore missing key", `{"equalToJson":{"id":"${json-unit.ignore}","a":1}}`, `{"a":1}`, true},
		{"placeholder any-string", `{"equalToJson":{"id":"${json-unit.any-string}"}}`, `{"id":"x"}`, true},
		{"placeholder any-string wrong type", `{"equalToJson":{"id":"${json-unit.any-string}"}}`, `{"id":1}`, false},
		{"placeholder any-number", `{"equalToJson":{"n":"${json-unit.any-number}"}}`, `{"n":3.5}`, true},
		{"placeholder any-boolean", `{"equalToJson":{"b":"${json-unit.any-boolean}"}}`, `{"b":false}`, true},
		{"placeholder regex", `{"equalToJson":{"id":"${json-unit.regex}[a-z]+-\\d+"}}`, `{"id":"ord-12"}`, true},
		{"placeholder regex miss", `{"equalToJson":{"id":"${json-unit.regex}[a-z]+"}}`, `{"id":"ord-12"}`, false},
		{"placeholder unknown is literal", `{"equalToJson":{"id":"${json-unit.other}"}}`, `{"id":"${json-unit.other}"}`, true},
		{"jsonPath present", `{"matchesJsonPath":"$.order.id"}`, `{"order":{"id":7}}`, true},
		{"jsonPath missing", `{"matchesJsonPath":"$.order.ref"}`, `{"order":{"id":7}}`, false},
		{"jsonPath empty array", `{"matchesJsonPath":"$.items"}`, `{"items":[]}`, false},
		{"jsonPath filter", `{"matchesJsonPath":"$.items[?(@.qty > 2)]"}`, `{"items":[{"qty":1},{"qty":3}]}`, true},
		{"jsonPath filter none", `{"matchesJsonPath":"$.items[?(@.qty > 5)]"}`, `{"items":[{"qty":1},{"qty":3}]}`, false},
		{"jsonPath equalTo", `{"matchesJsonPath":{"expression":"$.team","equalTo":"claims"}}`, `{"team":"claims"}`, true},
		{"jsonPath number", `{"matchesJsonPath":{"expression":"$.n","equalTo":"5"}}`, `{"n":5}`, true},
		{"jsonPath any of many", `{"matchesJsonPath":{"expression":"$.items[*].sku","equalTo":"B"}}`, `{"items":[{"sku":"A"},{"sku":"B"}]}`, true},
		{"jsonPath whole list as JSON", `{"matchesJsonPath":{"expression":"$.items[*].n","equalToJson":[1,2]}}`, `{"items":[{"n":1},{"n":2}]}`, true},
		{"jsonPath sub absent", `{"matchesJsonPath":{"expression":"$.gone","absent":true}}`, `{"a":1}`, true},
		{"jsonPath object sub", `{"matchesJsonPath":{"expression":"$.a","equalToJson":{"b":1}}}`, `{"a":{"b":1}}`, true},
		{"jsonPath not json", `{"matchesJsonPath":"$.a"}`, `nope`, false},
		{"jsonSchema", `{"matchesJsonSchema":{"type":"object","required":["id"]}}`, `{"id":1}`, true},
		{"jsonSchema fails", `{"matchesJsonSchema":{"type":"object","required":["id"]}}`, `{}`, false},
		{"jsonSchema string form", `{"matchesJsonSchema":"{\"type\":\"array\"}","schemaVersion":"V7"}`, `[]`, true},
		{"jsonSchema not json", `{"matchesJsonSchema":{"type":"object"}}`, `nope`, false},
		{"binaryEqualTo", `{"binaryEqualTo":"aGVsbG8="}`, `hello`, true},
		{"binaryEqualTo differs", `{"binaryEqualTo":"aGVsbG8="}`, `hellO`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := CompileContent(vp(t, tt.pattern), opts())
			if err != nil {
				t.Fatalf("CompileContent(%s) error: %v", tt.pattern, err)
			}
			if got := m.MatchContent(ContentOf([]byte(tt.body))).Matched; got != tt.want {
				t.Errorf("MatchContent(%s) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestCompileContentErrors(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		`{"equalToJson":"{not json"}`,
		`{"equalToJson":{"a":1},"ignoreArrayOrder":"yes"}`,
		`{"equalToJson":{"id":"${json-unit.regex}("}}`,
		`{"equalToJson":[{"id":"${json-unit.regex}("}]}`,
		`{"matchesJsonPath":5}`,
		`{"matchesJsonPath":{"equalTo":"x"}}`,
		`{"matchesJsonPath":"$[[["}`,
		`{"matchesJsonPath":{"expression":"$.a","bogus":1}}`,
		`{"matchesJsonSchema":"{nope"}`,
		`{"matchesJsonSchema":{"type":7}}`,
		`{"binaryEqualTo":5}`,
		`{"binaryEqualTo":"%%%"}`,
		`{"bogus":1}`,
	} {
		if _, err := CompileContent(vp(t, p), opts()); err == nil {
			t.Errorf("CompileContent(%s) succeeded, want an error", p)
		}
	}
	_, err := CompileContent(vp(t, `{"equalToXml":"<a/>"}`), opts())
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("equalToXml error = %v, want ErrNotImplemented", err)
	}
	for _, v := range []string{"V4", "V6", "V7", "V201909", "V202012", "other"} {
		if (draft(v) == nil) != (v == "other") {
			t.Errorf("draft(%s) = %v", v, draft(v))
		}
	}
}

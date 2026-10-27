package match

import (
	"encoding/json"
	"testing"

	"github.com/pwgith/shadow-mock/internal/model"
)

// Request bodies come from whoever can reach the mock port, so no body may make a matcher panic.
func FuzzMatchContent(f *testing.F) {
	patterns := []string{
		`{"equalToJson":{"a":1,"b":[1,2]},"ignoreArrayOrder":true}`,
		`{"matchesJsonPath":"$.items[?(@.qty > 2)]"}`,
		`{"matchesJsonPath":{"expression":"$..a","equalToJson":[1]}}`,
		`{"matchesJsonSchema":{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}}}}`,
		`{"equalToXml":"<a><b>${xmlunit.ignore}</b></a>"}`,
		`{"matchesXPath":"//b[text() > 1]"}`,
		`{"matchesXPath":{"expression":"count(//b)","equalTo":"2"}}`,
		`{"binaryEqualTo":"aGVsbG8="}`,
		`{"matches":"(a+)+b"}`,
		`{"contains":"x"}`,
	}
	bodies := []string{
		``, `{}`, `[]`, `nope`, `{"a":1,"b":[1,2]}`, `{"items":[{"qty":3}]}`, `<a><b>1</b><b>2</b></a>`,
		`<!DOCTYPE a [<!ENTITY x "y">]><a>&x;</a>`, `{"a":` + "\x00" + `}`, `[[[[[[[[[[[[[[[[[[[[1]]]]]]]]]]]]]]]]]]]`,
		`<a><a><a><a><a><a><a><a><a><a></a></a></a></a></a></a></a></a></a></a>`, `1e999999`,
	}
	compiled := make([]ContentMatcher, 0, len(patterns))
	for _, p := range patterns {
		m, err := CompileContent(pattern(f, p), opts())
		if err != nil {
			f.Fatalf("seed pattern %s: %v", p, err)
		}
		compiled = append(compiled, m)
		for _, b := range bodies {
			f.Add(len(compiled)-1, []byte(b))
		}
	}
	f.Fuzz(func(_ *testing.T, which int, body []byte) {
		if which < 0 || which >= len(compiled) {
			return
		}
		compiled[which].MatchContent(ContentOf(body))
	})
}

func pattern(tb testing.TB, s string) model.ValuePattern {
	tb.Helper()
	var p model.ValuePattern
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		tb.Fatalf("pattern %s: %v", s, err)
	}
	return p
}

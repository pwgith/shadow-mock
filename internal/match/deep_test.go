package match

import (
	"strings"
	"testing"
)

// A body at the size limit can be nested a million levels deep. Recursing over that would
// overflow the stack, which is a fatal error that stops the whole server and cannot be recovered.
func TestDeeplyNestedBodiesAreSafe(t *testing.T) {
	t.Parallel()
	const depth = 1_000_000
	xml := strings.Repeat("<a>", depth) + "x" + strings.Repeat("</a>", depth)
	jsonArr := strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth)
	jsonObj := strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth)

	for _, tt := range []struct{ name, pattern, body string }{
		{"xpath over deep xml", `{"matchesXPath":"//a"}`, xml},
		{"xpath with namespaces over deep xml", `{"matchesXPath":"//a","xPathNamespaces":{"p":"urn:x"}}`, xml},
		{"xpath count over deep xml", `{"matchesXPath":{"expression":"count(//a)","equalTo":"1"}}`, xml},
		{"equalToXml over deep xml", `{"equalToXml":"<a><a>x</a></a>"}`, xml},
		{"equalToJson over deep array", `{"equalToJson":[[1]]}`, jsonArr},
		{"equalToJson over deep object", `{"equalToJson":{"a":{"a":1}}}`, jsonObj},
		{"jsonPath over deep array", `{"matchesJsonPath":"$..*"}`, jsonArr},
		{"jsonPath recursive over deep object", `{"matchesJsonPath":"$..a"}`, jsonObj},
		{"json schema over deep array", `{"matchesJsonSchema":{"type":"array"}}`, jsonArr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := CompileContent(pattern(t, tt.pattern), opts())
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			m.MatchContent(ContentOf([]byte(tt.body))) // must return, not crash
		})
	}
}

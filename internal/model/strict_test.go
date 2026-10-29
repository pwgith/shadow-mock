package model

import (
	"errors"
	"testing"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

func pointerOf(t *testing.T, err error) string {
	t.Helper()
	var ve *apierr.ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) == 0 || ve.Errors[0].Source == nil {
		t.Fatalf("got %v, want a validation error with a pointer", err)
	}
	return ve.Errors[0].Source.Pointer
}

func TestCheckKnownFieldsPointsAtTheMisspelling(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, pointer string }{
		{"top level", `{"reqest":{},"request":{},"response":{}}`, "/reqest"},
		{"request", `{"request":{"urlPaht":"/a"},"response":{}}`, "/request/urlPaht"},
		{"response", `{"request":{},"response":{"statu":200}}`, "/response/statu"},
		{"delay distribution", `{"request":{},"response":{"delayDistribution":{"type":"lognormal","medin":1,"sigma":0.1}}}`, "/response/delayDistribution/medin"},
		{"a field of another distribution", `{"request":{},"response":{"delayDistribution":{"type":"lognormal","median":1,"sigma":0.1,"lower":1}}}`, "/response/delayDistribution/lower"},
		{"multipart pattern", `{"request":{"multipartPatterns":[{"matchingType":"ANY","bodyPatternz":[]}]},"response":{}}`, "/request/multipartPatterns/0/bodyPatternz"},
		{"basic auth", `{"request":{"basicAuthCredentials":{"username":"a","pasword":"b"}},"response":{}}`, "/request/basicAuthCredentials/pasword"},
		{"post serve action", `{"request":{},"response":{},"postServeActions":[{"name":"x","paramz":1}]}`, "/postServeActions/paramz"},
		{"call rule response", `{"request":{},"response":{},"callResponses":[{"calls":"1","response":{"statuz":1}}]}`, "/callResponses/0/response/statuz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := pointerOf(t, CheckKnownFields([]byte(tt.in))); got != tt.pointer {
				t.Errorf("pointer = %q, want %q", got, tt.pointer)
			}
		})
	}
}

func TestCheckKnownFieldsLeavesOpenObjectsAlone(t *testing.T) {
	t.Parallel()
	in := `{"id":"a","name":"n","priority":2,"persistent":true,"insertionIndex":4,
	  "metadata":{"anything":{"goes":1}},
	  "request":{"method":"GET","urlPath":"/a","headers":{"X":{"equalTo":"1","caseInsensitive":true}},
	    "bodyPatterns":[{"matchesJsonPath":{"expression":"$.a","contains":"b"}},{"and":[{"contains":"a"},{"not":{"contains":"b"}}]}],
	    "customMatcher":{"name":"x","parameters":{"any":1}}},
	  "response":{"status":200,"transformerParameters":{"any":1},"delayDistribution":{"type":"uniform","lower":1,"upper":2}},
	  "serveEventListeners":[{"name":"webhook","parameters":{"methd":"GET"}}],
	  "callResponses":[{"calls":"1","response":{"status":201}}]}`
	if err := CheckKnownFields([]byte(in)); err != nil {
		t.Fatal(err)
	}
}

func TestAMatcherWithoutAnOperatorIsAlwaysRejected(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, pointer string }{
		{"header", `{"request":{"headers":{"X":{"equalz":"1"}}},"response":{}}`, "/request/headers/X"},
		{"body pattern", `{"request":{"bodyPatterns":[{"equalToJsn":"1"}]},"response":{}}`, "/request/bodyPatterns/0"},
		{"inside and", `{"request":{"headers":{"X":{"and":[{"contains":"a"},{"nope":1}]}}},"response":{}}`, "/request/headers/X/and/1"},
		{"a bare string", `{"request":{"host":"/x"},"response":{}}`, "/request/host"},
		{"only an option", `{"request":{"queryParameters":{"q":{"caseInsensitive":true}}},"response":{}}`, "/request/queryParameters/q"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeStubMapping([]byte(tt.in))
			if got := pointerOf(t, err); got != tt.pointer {
				t.Errorf("pointer = %q, want %q", got, tt.pointer)
			}
		})
	}
}

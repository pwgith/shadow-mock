package fs

import (
	"testing"

	"github.com/pwgith/shadow-mock/internal/model"
)

// The expected names were written by WireMock 3.13.2 for the same mappings.
func TestFileNameIsWhatWireMockWrites(t *testing.T) {
	t.Parallel()
	const id = "11111111-1111-1111-1111-111111111111"
	tests := []struct{ name, in, want string }{
		{"a name", `{"name":"Hello World","request":{"url":"/x"}}`, "hello-world-" + id + ".json"},
		{"punctuation in a name", `{"name":"Get /users?id=1 (v2)","request":{"url":"/x"}}`, "get-usersid1-v2-" + id + ".json"},
		{"accents are dropped", `{"name":"Café Ünï","request":{"url":"/x"}}`, "caf-n-" + id + ".json"},
		{"dots, underscores and hyphens stay", `{"name":"a.b_c-d","request":{"url":"/x"}}`, "a.b_c-d-" + id + ".json"},
		{"no name uses the method and url", `{"request":{"urlPath":"/users/42"}}`, "any-users42-" + id + ".json"},
		{"an empty name is no name", `{"name":"","request":{"url":"/default"}}`, "any-default-" + id + ".json"},
		{"a method", `{"request":{"method":"POST","url":"/p"}}`, "post-p-" + id + ".json"},
		{"a pattern", `{"request":{"urlPathPattern":"/u/[0-9]+"}}`, "any-u0-9-" + id + ".json"},
		{"a query", `{"request":{"url":"/search?q=a b&x=1"}}`, "any-searchqa-bx1-" + id + ".json"},
		{"no url at all", `{"request":{"method":"GET"}}`, "get-always-" + id + ".json"},
		{"a url template", `{"request":{"urlPathTemplate":"/users/{id}"}}`, "any-usersid-" + id + ".json"},
		{"the root", `{"request":{"url":"/"}}`, "any--" + id + ".json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := model.DecodeStubMapping([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			m = m.WithID(id)
			if got := fileName(m); got != tt.want {
				t.Errorf("fileName = %q, want %q", got, tt.want)
			}
		})
	}
}

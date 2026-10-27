package match

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/model"
)

func rp(t *testing.T, s string) *model.RequestPattern {
	t.Helper()
	var p model.RequestPattern
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("pattern %s: %v", s, err)
	}
	return &p
}

func newReq(t *testing.T, method, target, body string, header map[string]string) *Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	for k, v := range header {
		r.Header.Set(k, v)
	}
	return NewRequest(r, []byte(body), r.URL.EscapedPath())
}

func TestRequestMatcher(t *testing.T) {
	t.Parallel()
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	tests := []struct {
		name    string
		pattern string
		method  string
		target  string
		body    string
		header  map[string]string
		want    bool
	}{
		{"empty pattern matches anything", `{}`, "DELETE", "/x?y=1", "", nil, true},
		{"method ANY", `{"method":"ANY","urlPath":"/a"}`, "PATCH", "/a", "", nil, true},
		{"method mismatch", `{"method":"POST","urlPath":"/a"}`, "GET", "/a", "", nil, false},
		{"url includes query", `{"url":"/a?b=1"}`, "GET", "/a?b=1", "", nil, true},
		{"url query differs", `{"url":"/a?b=1"}`, "GET", "/a?b=2", "", nil, false},
		{"urlPath ignores query", `{"urlPath":"/a"}`, "GET", "/a?b=2", "", nil, true},
		{"urlPattern", `{"urlPattern":"/a\\?b=\\d+"}`, "GET", "/a?b=42", "", nil, true},
		{"urlPathPattern", `{"urlPathPattern":"/claims/[0-9]+"}`, "GET", "/claims/12?x=1", "", nil, true},
		{"urlPathPattern miss", `{"urlPathPattern":"/claims/[0-9]+"}`, "GET", "/claims/ab", "", nil, false},
		{"template", `{"urlPathTemplate":"/claims/{id}/notes/{n}"}`, "GET", "/claims/7/notes/2", "", nil, true},
		{"template wrong length", `{"urlPathTemplate":"/claims/{id}"}`, "GET", "/claims/7/notes", "", nil, false},
		{"template literal differs", `{"urlPathTemplate":"/claims/{id}"}`, "GET", "/orders/7", "", nil, false},
		{"template empty segment", `{"urlPathTemplate":"/claims/{id}"}`, "GET", "/claims/", "", nil, false},
		{"path parameters", `{"urlPathTemplate":"/claims/{id}","pathParameters":{"id":{"matches":"[0-9]+"}}}`, "GET", "/claims/7", "", nil, true},
		{"path parameters miss", `{"urlPathTemplate":"/claims/{id}","pathParameters":{"id":{"matches":"[0-9]+"}}}`, "GET", "/claims/x", "", nil, false},
		{"query", `{"queryParameters":{"q":{"equalTo":"1"}}}`, "GET", "/a?q=1", "", nil, true},
		{"query absent", `{"queryParameters":{"q":{"absent":true}}}`, "GET", "/a", "", nil, true},
		{"query miss", `{"queryParameters":{"q":{"equalTo":"1"}}}`, "GET", "/a?q=2", "", nil, false},
		{"header case-insensitive name", `{"headers":{"x-trace":{"equalTo":"abc"}}}`, "GET", "/a", "", map[string]string{"X-Trace": "abc"}, true},
		{"header miss", `{"headers":{"X-Trace":{"equalTo":"abc"}}}`, "GET", "/a", "", nil, false},
		{"cookie", `{"cookies":{"session":{"equalTo":"s1"}}}`, "GET", "/a", "", map[string]string{"Cookie": "session=s1"}, true},
		{"cookie absent", `{"cookies":{"session":{"absent":true}}}`, "GET", "/a", "", nil, true},
		{"cookie miss", `{"cookies":{"session":{"equalTo":"s1"}}}`, "GET", "/a", "", nil, false},
		{"form", `{"formParameters":{"name":{"equalTo":"x y"}}}`, "POST", "/a", "name=x+y", form, true},
		{"form miss", `{"formParameters":{"name":{"equalTo":"z"}}}`, "POST", "/a", "name=x", form, false},
		{"form wrong type", `{"formParameters":{"name":{"equalTo":"x"}}}`, "POST", "/a", "name=x", nil, false},
		{"basic auth", `{"basicAuthCredentials":{"username":"u","password":"p"}}`, "GET", "/a", "", map[string]string{"Authorization": "Basic dTpw"}, true},
		{"basic auth miss", `{"basicAuthCredentials":{"username":"u","password":"p"}}`, "GET", "/a", "", nil, false},
		{"host", `{"host":{"equalTo":"example.com"}}`, "GET", "http://example.com/a", "", nil, true},
		{"host miss", `{"host":{"equalTo":"other.com"}}`, "GET", "http://example.com/a", "", nil, false},
		{"port", `{"port":8443}`, "GET", "http://example.com:8443/a", "", nil, true},
		{"port default", `{"port":80}`, "GET", "http://example.com/a", "", nil, true},
		{"port miss", `{"port":81}`, "GET", "http://example.com/a", "", nil, false},
		{"scheme", `{"scheme":"http"}`, "GET", "/a", "", nil, true},
		{"scheme miss", `{"scheme":"https"}`, "GET", "/a", "", nil, false},
		{"body", `{"bodyPatterns":[{"matchesJsonPath":"$.id"},{"contains":"7"}]}`, "POST", "/a", `{"id":7}`, nil, true},
		{"body miss", `{"bodyPatterns":[{"matchesJsonPath":"$.id"},{"contains":"8"}]}`, "POST", "/a", `{"id":7}`, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := Compile(rp(t, tt.pattern), opts())
			if err != nil {
				t.Fatalf("Compile(%s) error: %v", tt.pattern, err)
			}
			req := newReq(t, tt.method, tt.target, tt.body, tt.header)
			if got := m.Match(req); got != tt.want {
				t.Errorf("Match(%s %s) = %v, want %v", tt.method, tt.target, got, tt.want)
			}
			score := m.Score(req)
			if score.Matched != tt.want || (tt.want && score.Distance != 0) || (!tt.want && score.Distance <= 0) {
				t.Errorf("Score(%s %s) = %+v, inconsistent with Match = %v", tt.method, tt.target, score, tt.want)
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		`{"urlPattern":"("}`,
		`{"urlPathTemplate":"/a/{}"}`,
		`{"urlPathTemplate":"/a/x{id}"}`,
		`{"pathParameters":{"id":{"bogus":1}},"urlPathTemplate":"/a/{id}"}`,
		`{"queryParameters":{"q":{"bogus":1}}}`,
		`{"headers":{"h":{"bogus":1}}}`,
		`{"cookies":{"c":{"bogus":1}}}`,
		`{"formParameters":{"f":{"bogus":1}}}`,
		`{"host":{"bogus":1}}`,
		`{"bodyPatterns":[{"bogus":1}]}`,
		`{"multipartPatterns":[{"headers":{"h":{"bogus":1}}}]}`,
		`{"multipartPatterns":[{"bodyPatterns":[{"bogus":1}]}]}`,
		`{"customMatcher":{"name":"mine"}}`,
	} {
		if _, err := Compile(rp(t, p), opts()); err == nil {
			t.Errorf("Compile(%s) succeeded, want an error", p)
		}
	}
}

func multipartBody(t *testing.T, parts map[string]string) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, value := range parts {
		fw, err := w.CreateFormField(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), w.FormDataContentType()
}

func TestMultipart(t *testing.T) {
	t.Parallel()
	body, ct := multipartBody(t, map[string]string{"meta": `{"kind":"claim"}`, "file": "PDF"})
	header := map[string]string{"Content-Type": ct}
	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"any part", `{"multipartPatterns":[{"matchingType":"ANY","bodyPatterns":[{"equalTo":"PDF"}]}]}`, true},
		{"by header", `{"multipartPatterns":[{"headers":{"Content-Disposition":{"contains":"name=\"meta\""}},"bodyPatterns":[{"matchesJsonPath":"$.kind"}]}]}`, true},
		{"all parts", `{"multipartPatterns":[{"matchingType":"ALL","bodyPatterns":[{"matches":".+"}]}]}`, true},
		{"all parts miss", `{"multipartPatterns":[{"matchingType":"ALL","bodyPatterns":[{"equalTo":"PDF"}]}]}`, false},
		{"no part", `{"multipartPatterns":[{"bodyPatterns":[{"equalTo":"XLS"}]}]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := Compile(rp(t, tt.pattern), opts())
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Match(newReq(t, "POST", "/upload", body, header)); got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
		})
	}
	m, err := Compile(rp(t, tests[0].pattern), opts())
	if err != nil {
		t.Fatal(err)
	}
	if m.Match(newReq(t, "POST", "/upload", "plain", nil)) {
		t.Error("multipart pattern matched a non-multipart body")
	}
}

func TestRequestViews(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), "GET", "https://api.test/a%20b?x=1&x=2", http.NoBody)
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Cookie", "a=1; a=2")
	r.RemoteAddr = "10.0.0.9:5555"
	req := NewRequest(r, nil, r.URL.EscapedPath())
	if req.Scheme != "https" || req.Port != 443 || req.Host != "api.test" || req.ClientIP != "10.0.0.9" {
		t.Errorf("NewRequest = %+v", req)
	}
	if req.URL() != "/a%20b?x=1&x=2" {
		t.Errorf("URL() = %q", req.URL())
	}
	if diff := cmp.Diff([]string{"1", "2"}, req.Cookies["a"]); diff != "" {
		t.Errorf("cookies (-want +got):\n%s", diff)
	}
	if _, ok := req.JSON(); ok {
		t.Error("JSON() of an empty body should fail")
	}

	s := FromSample(&model.SampleRequest{Method: "post", URL: "/s?q=1", Headers: map[string]string{"Host": "h:81"}, Cookies: map[string]string{"c": "v"}, BodyAsBase64: "eyJhIjpbMSwyXX0="})
	if s.Method != http.MethodPost || s.Path != "/s" || s.Query.Get("q") != "1" || s.Port != 81 || s.Cookies["c"][0] != "v" {
		t.Errorf("FromSample = %+v", s)
	}
	v, ok := s.JSON()
	if !ok {
		t.Fatal("FromSample body should parse as JSON")
	}
	if diff := cmp.Diff(map[string]any{"a": []any{1.0, 2.0}}, v); diff != "" {
		t.Errorf("JSON (-want +got):\n%s", diff)
	}
	if p, port := splitHostPort("h:x", "http"); p != "h" || port != 0 {
		t.Errorf("splitHostPort(h:x) = %s, %d", p, port)
	}
	if clientIP("nonsense") != "nonsense" {
		t.Error("clientIP should return an unparseable address unchanged")
	}
}

func TestPathParamsAndLiteralPath(t *testing.T) {
	t.Parallel()
	m, err := Compile(rp(t, `{"urlPathTemplate":"/claims/{id}"}`), opts())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]string{"id": "a b"}, m.PathParams(newReq(t, "GET", "/claims/a%20b", "", nil))); diff != "" {
		t.Errorf("PathParams (-want +got):\n%s", diff)
	}
	if _, ok := m.LiteralPath(); ok {
		t.Error("a template has no literal path")
	}
	for p, want := range map[string]string{`{"urlPath":"/a"}`: "/a", `{"url":"/b?c=1"}`: "/b"} {
		m, err := Compile(rp(t, p), opts())
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := m.LiteralPath(); !ok || got != want {
			t.Errorf("LiteralPath(%s) = %q, %v, want %q", p, got, ok, want)
		}
		if m.PathParams(newReq(t, "GET", "/a", "", nil)) != nil {
			t.Error("PathParams without a template should be nil")
		}
	}
	if (&RequestMatcher{method: "GET"}).Method() != "GET" {
		t.Error("Method()")
	}
}

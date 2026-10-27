package respond_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/store"
)

func input(t *testing.T, method, target, body string, params map[string]string, headers ...string) *respond.Input {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "http://shadow.example:8080"+target, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Add(headers[i], headers[i+1])
	}
	return &respond.Input{Request: match.NewRequest(r, []byte(body), r.URL.EscapedPath()), PathParams: params, CallNumber: 3}
}

func TestTemplatedResponses(t *testing.T) {
	t.Parallel()
	in := input(t, http.MethodPost, "/t/claims/42?status=O&tag=a&tag=b", `{"amount":15}`,
		map[string]string{"id": "42"}, "X-Corr", "c-9", "Cookie", "sid=s-1")
	tests := []struct{ name, def, want string }{
		{"method", `{"body":"{{request.method}}","transformers":["response-template"]}`, "POST"},
		{"path", `{"body":"{{request.path}}","transformers":["response-template"]}`, "/t/claims/42"},
		{"path parameter", `{"body":"{{request.path.id}}","transformers":["response-template"]}`, "42"},
		{"path still prints with parameters", `{"body":"[{{request.path}}]","transformers":["response-template"]}`, "[/t/claims/42]"},
		{"segment", `{"body":"{{request.pathSegments.[2]}}","transformers":["response-template"]}`, "42"},
		{"url", `{"body":"{{request.url}}","transformers":["response-template"]}`, "/t/claims/42?status=O&tag=a&tag=b"},
		{"query", `{"body":"{{request.query.status}}","transformers":["response-template"]}`, "O"},
		{"repeated query value by index", `{"body":"{{request.query.tag.[1]}}","transformers":["response-template"]}`, "b"},
		{"repeated query value first", `{"body":"{{request.query.tag.first}} {{request.query.tag.last}}","transformers":["response-template"]}`, "a b"},
		{"header", `{"body":"{{request.headers.X-Corr}}","transformers":["response-template"]}`, "c-9"},
		{"one query value is reachable as first, last and by index", `{"body":"{{request.query.status.first}}{{request.query.status.last}}{{request.query.status.[0]}}","transformers":["response-template"]}`, "OOO"},
		{"toJson writes one query value as a string", `{"body":"{{toJson request.query.status}}","transformers":["response-template"]}`, `"O"`},
		{"toJson writes repeated query values as a list", `{"body":"{{toJson request.query.tag}}","transformers":["response-template"]}`, `["a","b"]`},
		{"the Host header is a header", `{"body":"{{request.headers.Host}}","transformers":["response-template"]}`, "shadow.example:8080"},
		{"toJson lists each header once", `{"body":"{{toJson request.headers}}","transformers":["response-template"]}`, `{"Cookie":"sid=s-1","Host":"shadow.example:8080","X-Corr":"c-9"}`},
		{"header in lower case", `{"body":"{{request.headers.x-corr}}","transformers":["response-template"]}`, "c-9"},
		{"cookie", `{"body":"{{request.cookies.sid}}","transformers":["response-template"]}`, "s-1"},
		{"body helper", `{"body":"{{jsonPath request.body '$.amount'}}","transformers":["response-template"]}`, "15"},
		{"host, port and base URL", `{"body":"{{request.host}}:{{request.port}} {{request.baseUrl}}","transformers":["response-template"]}`, "shadow.example:8080 http://shadow.example:8080"},
		{"call number", `{"body":"{{request.callNumber}}","transformers":["response-template"]}`, "3"},
		{"transformer parameters", `{"body":"{{parameters.greeting}}","transformers":["response-template"],"transformerParameters":{"greeting":"hi"}}`, "hi"},
		{"JSON body", `{"jsonBody":{"m":"{{request.method}}"},"transformers":["response-template"]}`, `{"m":"POST"}`},
		{"header values", `{"headers":{"X-Claim":"{{request.path.id}}"},"transformers":["response-template"]}`, ""},
		{"not rendered without the transformer", `{"body":"{{request.method}}"}`, "{{request.method}}"},
		{"base64 bodies are not templates", `{"base64Body":"e3tyZXF1ZXN0Lm1ldGhvZH19","transformers":["response-template"]}`, "{{request.method}}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _, _ := newRenderer(t, false)
			got, err := r.Render(t.Context(), def(t, tt.def), nil, in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Body) != tt.want {
				t.Errorf("body = %q, want %q", got.Body, tt.want)
			}
			if tt.name == "header values" && got.Header.Get("X-Claim") != "42" {
				t.Errorf("X-Claim = %q, want 42", got.Header.Get("X-Claim"))
			}
		})
	}
}

func TestTemplatingIsOffUnlessAsked(t *testing.T) {
	t.Parallel()
	in := input(t, http.MethodGet, "/g", "", nil)
	r, _, _ := newRenderer(t, false)
	d := def(t, `{"body":"{{request.path}}"}`)
	if got, _ := r.Render(t.Context(), d, nil, in); string(got.Body) != "{{request.path}}" {
		t.Errorf("body = %q, want the template left as written", got.Body)
	}
	r.Templating = true
	if got, _ := r.Render(t.Context(), d, nil, in); string(got.Body) != "/g" {
		t.Errorf("with global templating body = %q, want /g", got.Body)
	}
	if got, _ := r.Render(t.Context(), d, nil, nil); string(got.Body) != "{{request.path}}" {
		t.Errorf("without a request body = %q, want the template left as written", got.Body)
	}
}

func TestTemplatedBodyFiles(t *testing.T) {
	t.Parallel()
	r, files, _ := newRenderer(t, false)
	ctx := t.Context()
	if err := files.Put(ctx, globalScope, "claim-42.json", []byte(`{"id":"{{request.path.id}}"}`)); err != nil {
		t.Fatal(err)
	}
	in := input(t, http.MethodGet, "/claims/42", "", map[string]string{"id": "42"})
	got, err := r.Render(ctx, def(t, `{"bodyFileName":"claim-{{request.path.id}}.json","transformers":["response-template"]}`), []store.Scope{globalScope}, in)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != `{"id":"42"}` {
		t.Errorf("body = %q, want the file with its template rendered", got.Body)
	}
	// A name that leaves __files is not found, and nothing outside is read.
	_, err = r.Render(ctx, def(t, `{"bodyFileName":"../../etc/passwd","transformers":["response-template"]}`), []store.Scope{globalScope}, in)
	if err == nil {
		t.Error("a body file outside __files was served")
	}
}

func TestBrokenTemplatesAreServerErrors(t *testing.T) {
	t.Parallel()
	in := input(t, http.MethodGet, "/x", "", nil)
	r, _, _ := newRenderer(t, false)
	for _, js := range []string{
		`{"body":"{{#if}}","transformers":["response-template"]}`,
		`{"body":"{{math 1 '/' 0}}","transformers":["response-template"]}`,
		`{"headers":{"X":"{{math 1 '/' 0}}"},"transformers":["response-template"]}`,
	} {
		if _, err := r.Render(t.Context(), def(t, js), nil, in); err == nil {
			t.Errorf("Render(%s) succeeded, want a template error", js)
		}
	}
}

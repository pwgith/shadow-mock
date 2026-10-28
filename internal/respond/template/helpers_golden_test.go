package template_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/respond"
)

// goldenHelper is a template, the request it renders against, and what a real WireMock 3.13.2
// rendered for it (testdata/wiremock_helpers.json). Error means WireMock failed the render; Skip
// names a deliberate difference.
type goldenHelper struct {
	Name        string            `json:"name"`
	Tpl         string            `json:"tpl"`
	Request     string            `json:"request"`
	Path        string            `json:"path"`
	Method      string            `json:"method"`
	ContentType string            `json:"contentType"`
	Headers     map[string]string `json:"headers"`
	Want        string            `json:"want"`
	Error       bool              `json:"error"`
	Skip        string            `json:"skip"`
}

// request builds the request the case was recorded with.
func (g goldenHelper) request(t *testing.T) *match.Request {
	t.Helper()
	path, method, contentType := g.Path, g.Method, g.ContentType
	if path == "" {
		path = "/t"
	}
	if method == "" {
		method = http.MethodPost
	}
	if contentType == "" {
		contentType = "application/json"
	}
	u, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{"Content-Type": {contentType}}
	for k, v := range g.Headers {
		h.Set(k, v)
	}
	cookies := map[string][]string{}
	for _, c := range (&http.Request{Header: h}).Cookies() {
		cookies[c.Name] = append(cookies[c.Name], c.Value)
	}
	body := []byte(g.Request)
	if method == http.MethodGet {
		body = nil
	}
	return &match.Request{
		Method: method, Scheme: "http", Host: "localhost", Port: 18200,
		Path: u.Path, RawQuery: u.RawQuery, Query: u.Query(), Header: h, Cookies: cookies, Body: body,
	}
}

func TestHelpersRenderAsWireMockDoes(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/wiremock_helpers.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenHelper
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()
			if tt.Skip != "" {
				t.Skip(tt.Skip)
			}
			data := respond.Data(&respond.Input{Request: tt.request(t)}, nil)
			got, err := engine().Render(tt.Tpl, data)
			if tt.Error {
				if err == nil {
					t.Fatalf("Render(%q) = %q, want an error as WireMock gives", tt.Tpl, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render(%q): %v", tt.Tpl, err)
			}
			got = strings.ReplaceAll(got, "\r\n", "\n")
			if got != tt.Want {
				t.Errorf("Render(%q)\n got %q\nwant %q", tt.Tpl, got, tt.Want)
			}
		})
	}
}

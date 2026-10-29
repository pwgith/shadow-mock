package fs

import (
	"strings"

	"github.com/pwgith/shadow-mock/internal/model"
)

// Namer renders a filename template (--filename-template) for a mapping, given FilenameData.
type Namer func(data map[string]any) (string, error)

// FilenameData is what a filename template can use: id, name, method and url, also under request.
func FilenameData(m model.StubMapping) map[string]any {
	method := m.Request.Method
	if method == "" {
		method = "ANY"
	}
	url := urlOf(&m.Request)
	return map[string]any{
		"id": m.ID, "name": m.Name, "method": method, "url": url,
		"request": map[string]any{"method": method, "url": url},
	}
}

// fileName is the name WireMock gives the file of a mapping it writes: the mapping's name, or the
// method and URL when it has none, then the id (FR-10.8).
func fileName(m model.StubMapping) string {
	base := slug(m.Name)
	if base == "" {
		method := slug(m.Request.Method)
		if method == "" {
			method = "any"
		}
		base = method + "-" + slug(urlOf(&m.Request))
	}
	return base + "-" + m.ID + ".json"
}

// urlOf is the URL a pattern matches on, whichever way it is written; "always" when it names none.
func urlOf(p *model.RequestPattern) string {
	for _, u := range []*string{p.URL, p.URLPath, p.URLPattern, p.URLPathPattern, p.URLPathTemplate} {
		if u != nil {
			return *u
		}
	}
	return "always"
}

// slug lower-cases s, turns white space into hyphens and drops everything but letters, digits,
// dots, underscores and hyphens, which is what WireMock does to a file name.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ', r == '\t', r == '\n', r == '\r':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Package template renders response templates: Handlebars with WireMock's helper set
// (FR-3.4). Templates are parsed once and cached. They can read only the data they are
// given, so they cannot reach the file system or the network (NFR-5.3).
package template

import (
	"fmt"
	"html"
	"strings"
	"sync"

	"github.com/mailgun/raymond/v2"

	"github.com/pwgith/shadow-mock/internal/clock"
)

// maxCached bounds the parsed-template cache. When it fills, it is emptied rather than
// tracking use: templates are re-parsed on demand and parsing is cheap next to a request.
const maxCached = 2048

// Engine parses and renders templates.
type Engine struct {
	clock clock.Clock

	mu    sync.RWMutex
	cache map[string]*compiled
}

// compiled is a parsed template.
type compiled struct {
	tpl *raymond.Template
}

// varsKey names the private data every render carries: the map that assign, parseJson, val and
// the other assigning forms write their variables to. It is the render's root context, so a
// variable set anywhere in the template is read back as an ordinary name.
const varsKey = "vars"

// New returns an engine whose time helpers read c.
func New(c clock.Clock) *Engine {
	return &Engine{clock: c, cache: map[string]*compiled{}}
}

// Render renders src with data as the root context. Text without any "{{" is returned as is.
func (e *Engine) Render(src string, data map[string]any) (out string, err error) {
	if !strings.Contains(src, "{{") {
		return src, nil
	}
	// raymond turns a helper's error into a returned error, but a runtime error inside a helper
	// or in raymond itself (a nil value where one was expected) still panics. Request data
	// reaches templates, and webhooks render in a goroutine that would take the server down.
	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("render template: %v", r)
		}
	}()
	c, err := e.compile(src)
	if err != nil {
		return "", err
	}
	frame := raymond.NewDataFrame()
	frame.Set(varsKey, data)
	rendered, err := c.tpl.ExecWith(data, frame)
	if err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return unmark(rendered), nil
}

func (e *Engine) compile(src string) (*compiled, error) {
	e.mu.RLock()
	c, ok := e.cache[src]
	e.mu.RUnlock()
	if ok {
		return c, nil
	}
	main, inline := extractInline(src)
	plain := rewrite(main)
	known := e.helperNames()
	if err := checkHelpers(plain, known); err != nil {
		return nil, err
	}
	t, err := raymond.Parse(plain)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	t.RegisterHelpers(e.helpers())
	for _, p := range inline {
		body := rewrite(p.body)
		if err := checkHelpers(body, known); err != nil {
			return nil, fmt.Errorf("partial %q: %w", p.name, err)
		}
		pt, perr := raymond.Parse(body)
		if perr != nil {
			return nil, fmt.Errorf("parse partial %q: %w", p.name, perr)
		}
		pt.RegisterHelpers(e.helpers())
		t.RegisterPartialTemplate(p.name, pt)
	}
	c = &compiled{tpl: t}
	e.mu.Lock()
	if len(e.cache) >= maxCached {
		clear(e.cache)
	}
	e.cache[src] = c
	e.mu.Unlock()
	return c, nil
}

// Safe marks every string in v as already safe, so the engine does not HTML-escape it:
// mock bodies are JSON, XML and plain text, not HTML, as in WireMock.
func Safe(v any) any {
	switch t := v.(type) {
	case string:
		return raymond.SafeString(t)
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = raymond.SafeString(s)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Safe(x)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = Safe(x)
		}
		return out
	case Headers:
		out := make(Headers, len(t))
		for k, x := range t {
			out[k] = Safe(x)
		}
		return out
	default:
		return v
	}
}

// Values the engine prints through fmt.Stringer, such as lists and objects, are HTML-escaped
// by raymond, which cannot be turned off. They wrap their text in these markers instead, and
// Render removes the markers and undoes the escaping inside them.
const (
	markStart = ""
	markEnd   = ""
)

// Mark wraps text that a fmt.Stringer returns, so it is printed as it is.
func Mark(s string) string { return markStart + s + markEnd }

// unmark removes the markers from rendered output and unescapes what is between them.
func unmark(s string) string {
	if !strings.Contains(s, markStart) {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, markStart)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.Index(s[i:], markEnd)
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(html.UnescapeString(s[i+len(markStart) : i+j]))
		s = s[i+j+len(markEnd):]
	}
}

// helperNames lists the helpers a template may call.
func (e *Engine) helperNames() map[string]bool {
	names := map[string]bool{}
	for name := range e.helpers() {
		names[name] = true
	}
	return names
}

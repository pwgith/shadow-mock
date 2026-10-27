// Package extension is the public API for extending Shadow Mock. WireMock's Java extensions
// are replaced by Go values registered when the binary is built (FR-2.7): a program that
// embeds Shadow Mock passes its matchers to the application at start-up.
//
// This package imports nothing from the engine, so extensions depend only on it.
package extension

import (
	"net/http"
	"net/url"
)

// Request is the part of a request a custom matcher can read. It is a copy, so a matcher
// cannot change what the engine goes on to match or journal.
type Request struct {
	Method string
	// URL is the path and query, as WireMock's url matcher sees them.
	URL    string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// RequestMatcher decides whether a request matches. A mapping asks for it by name:
//
//	"request": { "urlPath": "/x", "customMatcher": { "name": "is-even-id", "parameters": {} } }
//
// Match must be fast and safe to call from many goroutines at once, and must not block.
type RequestMatcher interface {
	// Name is the name mappings use. It must be unique.
	Name() string
	// Match reports whether the request matches, given the mapping's parameters.
	Match(r Request, parameters map[string]any) bool
}

// MatcherFunc builds a RequestMatcher from a function.
func MatcherFunc(name string, f func(r Request, parameters map[string]any) bool) RequestMatcher {
	return matcherFunc{name: name, f: f}
}

type matcherFunc struct {
	name string
	f    func(Request, map[string]any) bool
}

// Name returns the matcher's name.
func (m matcherFunc) Name() string { return m.name }

// Match calls the function.
func (m matcherFunc) Match(r Request, parameters map[string]any) bool { return m.f(r, parameters) }

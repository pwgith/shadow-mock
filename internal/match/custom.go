package match

import "github.com/pwgith/shadow-mock/pkg/extension"

// customMatcher is a mapping's use of a matcher registered through pkg/extension.
type customMatcher struct {
	name       string
	matcher    extension.RequestMatcher
	parameters map[string]any
}

func (c *customMatcher) matches(r *Request) bool {
	return c.matcher.Match(extension.Request{
		Method: r.Method, URL: r.URL(), Path: r.Path, Query: r.Query, Header: r.Header.Clone(),
		Body: append([]byte(nil), r.Body...),
	}, c.parameters)
}

package match

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/pwgith/shadow-mock/internal/model"
)

// urlMatcher matches the URL and extracts path template parameters (FR-2.1).
type urlMatcher struct {
	kind     string // url, urlPath, urlPattern, urlPathPattern, urlPathTemplate, or "" for any
	literal  string
	re       *regexp.Regexp
	template []templateSegment
}

type templateSegment struct {
	literal string
	param   string // set for {name} segments
}

func compileURL(p *model.RequestPattern) (*urlMatcher, error) {
	switch {
	case p.URL != nil:
		return &urlMatcher{kind: "url", literal: *p.URL}, nil
	case p.URLPath != nil:
		return &urlMatcher{kind: "urlPath", literal: *p.URLPath}, nil
	case p.URLPattern != nil, p.URLPathPattern != nil:
		kind, expr := "urlPattern", p.URLPattern
		if p.URLPathPattern != nil {
			kind, expr = "urlPathPattern", p.URLPathPattern
		}
		re, err := regexp.Compile("^(?:" + *expr + ")$")
		if err != nil {
			return nil, fmt.Errorf("%s: invalid regular expression: %w", kind, err)
		}
		return &urlMatcher{kind: kind, re: re}, nil
	case p.URLPathTemplate != nil:
		segs, err := parseTemplate(*p.URLPathTemplate)
		if err != nil {
			return nil, err
		}
		return &urlMatcher{kind: "urlPathTemplate", literal: *p.URLPathTemplate, template: segs}, nil
	default:
		return &urlMatcher{}, nil
	}
}

func parseTemplate(t string) ([]templateSegment, error) {
	parts := strings.Split(strings.TrimPrefix(t, "/"), "/")
	segs := make([]templateSegment, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := part[1 : len(part)-1]
			if name == "" {
				return nil, fmt.Errorf("urlPathTemplate %q has an empty parameter name", t)
			}
			segs = append(segs, templateSegment{param: name})
			continue
		}
		if strings.ContainsAny(part, "{}") {
			return nil, fmt.Errorf("urlPathTemplate %q: parameters must be whole path segments", t)
		}
		segs = append(segs, templateSegment{literal: part})
	}
	return segs, nil
}

// match returns the result and, for templates, the path parameters.
func (u *urlMatcher) match(r *Request) (Result, map[string]string) {
	switch u.kind {
	case "url":
		return equalDistance(r.URL(), u.literal), nil
	case "urlPath":
		return equalDistance(r.Path, u.literal), nil
	case "urlPattern":
		return boolResult(u.re.MatchString(r.URL())), nil
	case "urlPathPattern":
		return boolResult(u.re.MatchString(r.Path)), nil
	case "urlPathTemplate":
		params, ok := u.matchTemplate(r.Path)
		if !ok {
			return Result{Distance: textDistance(r.Path, u.literal)}, nil
		}
		return hit(), params
	default:
		return hit(), nil
	}
}

func (u *urlMatcher) matchTemplate(path string) (map[string]string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != len(u.template) {
		return nil, false
	}
	params := map[string]string{}
	for i, seg := range u.template {
		if seg.param == "" {
			if parts[i] != seg.literal {
				return nil, false
			}
			continue
		}
		if parts[i] == "" {
			return nil, false
		}
		v, err := url.PathUnescape(parts[i])
		if err != nil {
			v = parts[i]
		}
		params[seg.param] = v
	}
	return params, true
}

// PathParams returns the parameters of a urlPathTemplate for the request, for templating.
func (u *urlMatcher) PathParams(r *Request) map[string]string {
	if u.kind != "urlPathTemplate" {
		return nil
	}
	params, _ := u.matchTemplate(r.Path)
	return params
}

func equalDistance(got, want string) Result {
	if got == want {
		return hit()
	}
	return Result{Distance: textDistance(got, want)}
}

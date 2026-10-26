package model

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

// ValuePattern is one matcher object as written, for example {"equalTo":"x","caseInsensitive":true}.
// The match package compiles it.
type ValuePattern map[string]json.RawMessage

// BasicAuth is a basicAuthCredentials matcher.
type BasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// MultipartPattern matches parts of a multipart body.
type MultipartPattern struct {
	Name         string                  `json:"name,omitempty"`
	MatchingType string                  `json:"matchingType,omitempty"`
	Headers      map[string]ValuePattern `json:"headers,omitempty"`
	BodyPatterns []ValuePattern          `json:"bodyPatterns,omitempty"`
}

// CustomMatcher names a matcher registered through pkg/extension.
type CustomMatcher struct {
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// RequestPattern is a WireMock request pattern.
type RequestPattern struct {
	Method               string                  `json:"method,omitempty"`
	URL                  *string                 `json:"url,omitempty"`
	URLPath              *string                 `json:"urlPath,omitempty"`
	URLPattern           *string                 `json:"urlPattern,omitempty"`
	URLPathPattern       *string                 `json:"urlPathPattern,omitempty"`
	URLPathTemplate      *string                 `json:"urlPathTemplate,omitempty"`
	PathParameters       map[string]ValuePattern `json:"pathParameters,omitempty"`
	QueryParameters      map[string]ValuePattern `json:"queryParameters,omitempty"`
	Headers              map[string]ValuePattern `json:"headers,omitempty"`
	Cookies              map[string]ValuePattern `json:"cookies,omitempty"`
	FormParameters       map[string]ValuePattern `json:"formParameters,omitempty"`
	BasicAuthCredentials *BasicAuth              `json:"basicAuthCredentials,omitempty"`
	Host                 ValuePattern            `json:"host,omitempty"`
	Port                 *int                    `json:"port,omitempty"`
	Scheme               string                  `json:"scheme,omitempty"`
	BodyPatterns         []ValuePattern          `json:"bodyPatterns,omitempty"`
	MultipartPatterns    []MultipartPattern      `json:"multipartPatterns,omitempty"`
	CustomMatcher        *CustomMatcher          `json:"customMatcher,omitempty"`

	raw json.RawMessage
}

// requestPatternFields avoids recursion in UnmarshalJSON.
type requestPatternFields RequestPattern

// UnmarshalJSON decodes the pattern and keeps the original JSON.
func (p *RequestPattern) UnmarshalJSON(data []byte) error {
	var f requestPatternFields
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("request pattern: %w", err)
	}
	*p = RequestPattern(f)
	p.raw = slices.Clone(data)
	return nil
}

// MarshalJSON returns the pattern as received, including unknown fields.
func (p RequestPattern) MarshalJSON() ([]byte, error) {
	if p.raw != nil {
		return p.raw, nil
	}
	b, err := json.Marshal(requestPatternFields(p))
	if err != nil {
		return nil, fmt.Errorf("marshal request pattern: %w", err)
	}
	return b, nil
}

// Validate checks rules the JSON shape alone cannot express.
func (p *RequestPattern) Validate(ptr string) error {
	set := 0
	for _, u := range []*string{p.URL, p.URLPath, p.URLPattern, p.URLPathPattern, p.URLPathTemplate} {
		if u != nil {
			set++
		}
	}
	if set > 1 {
		return apierr.Invalid(ptr, "set at most one of url, urlPath, urlPattern, urlPathPattern and urlPathTemplate")
	}
	if len(p.PathParameters) > 0 && p.URLPathTemplate == nil {
		return apierr.Invalid(ptr+"/pathParameters", "pathParameters need urlPathTemplate")
	}
	if p.Scheme != "" && p.Scheme != "http" && p.Scheme != "https" {
		return apierr.Invalid(ptr+"/scheme", "scheme must be http or https")
	}
	return nil
}

// AnyMethod reports whether the pattern matches every method.
func (p *RequestPattern) AnyMethod() bool {
	return p.Method == "" || strings.EqualFold(p.Method, "ANY")
}

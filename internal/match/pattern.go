package match

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/pwgith/shadow-mock/internal/model"
)

// RequestMatcher is a compiled RequestPattern.
type RequestMatcher struct {
	method     string // upper case; "" matches any method
	url        *urlMatcher
	pathParams []namedString
	query      []namedMulti
	headers    []namedMulti
	cookies    []namedString
	form       []namedMulti
	basicAuth  string // expected "Basic …" header value, or ""
	host       StringMatcher
	port       int
	scheme     string
	bodies     []ContentMatcher
	multipart  []*multipartMatcher
}

// Compile compiles a request pattern. Errors describe the invalid part.
func Compile(p *model.RequestPattern, o Options) (*RequestMatcher, error) {
	u, err := compileURL(p)
	if err != nil {
		return nil, err
	}
	m := &RequestMatcher{url: u, scheme: p.Scheme}
	if !p.AnyMethod() {
		m.method = strings.ToUpper(p.Method)
	}
	if p.Port != nil {
		m.port = *p.Port
	}
	if m.pathParams, err = compileStrings("pathParameters", p.PathParameters, o); err != nil {
		return nil, err
	}
	if m.cookies, err = compileStrings("cookies", p.Cookies, o); err != nil {
		return nil, err
	}
	if m.query, err = compileMultis("queryParameters", p.QueryParameters, o); err != nil {
		return nil, err
	}
	if m.headers, err = compileMultis("headers", p.Headers, o); err != nil {
		return nil, err
	}
	if m.form, err = compileMultis("formParameters", p.FormParameters, o); err != nil {
		return nil, err
	}
	if err := m.compileRest(p, o); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *RequestMatcher) compileRest(p *model.RequestPattern, o Options) error {
	if p.BasicAuthCredentials != nil {
		creds := p.BasicAuthCredentials.Username + ":" + p.BasicAuthCredentials.Password
		m.basicAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))
	}
	if len(p.Host) > 0 {
		h, err := CompileString(p.Host, o)
		if err != nil {
			return fmt.Errorf("host: %w", err)
		}
		m.host = h
	}
	for i, bp := range p.BodyPatterns {
		cm, err := CompileContent(bp, o)
		if err != nil {
			return fmt.Errorf("bodyPatterns[%d]: %w", i, err)
		}
		m.bodies = append(m.bodies, cm)
	}
	for i, mp := range p.MultipartPatterns {
		mm, err := compileMultipart(mp, o)
		if err != nil {
			return fmt.Errorf("multipartPatterns[%d]: %w", i, err)
		}
		m.multipart = append(m.multipart, mm)
	}
	if p.CustomMatcher != nil {
		return fmt.Errorf("customMatcher %q: custom matchers are %w", p.CustomMatcher.Name, ErrNotImplemented)
	}
	return nil
}

// namedString and namedMulti keep matchers in name order, so evaluation is deterministic and
// allocates nothing.
type namedString struct {
	name string
	m    StringMatcher
}

type namedMulti struct {
	name string
	m    MultiMatcher
}

func compileStrings(field string, in map[string]model.ValuePattern, o Options) ([]namedString, error) {
	out := make([]namedString, 0, len(in))
	for _, k := range sortedKeys(in) {
		sm, err := CompileString(in[k], o)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", field, k, err)
		}
		out = append(out, namedString{name: k, m: sm})
	}
	return out, nil
}

func compileMultis(field string, in map[string]model.ValuePattern, o Options) ([]namedMulti, error) {
	out := make([]namedMulti, 0, len(in))
	for _, k := range sortedKeys(in) {
		mm, err := CompileMulti(in[k], o)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", field, k, err)
		}
		out = append(out, namedMulti{name: k, m: mm})
	}
	return out, nil
}

// Method returns the method the pattern requires, or "" for any.
func (m *RequestMatcher) Method() string { return m.method }

// LiteralPath returns the exact path the pattern requires, if it requires one. The engine
// uses it to bucket stubs so most requests evaluate only a few candidates.
func (m *RequestMatcher) LiteralPath() (string, bool) {
	switch m.url.kind {
	case "urlPath":
		return m.url.literal, true
	case "url":
		path, _, _ := strings.Cut(m.url.literal, "?")
		return path, true
	default:
		return "", false
	}
}

// PathParams returns the urlPathTemplate parameters of a request.
func (m *RequestMatcher) PathParams(r *Request) map[string]string { return m.url.PathParams(r) }

// Match reports whether the request matches, stopping at the first failure.
func (m *RequestMatcher) Match(r *Request) bool { return m.eval(r, false).Matched }

// Mismatches evaluates every part of the pattern and names those that did not match, such as
// "Header [Accept]", for the 404 diagnostic.
func (m *RequestMatcher) Mismatches(r *Request) []string {
	s := &scorer{full: true}
	m.evalInto(r, s)
	return s.mismatched
}

// Score evaluates every part of the pattern and returns the overall distance, for near misses.
func (m *RequestMatcher) Score(r *Request) Result { return m.eval(r, true) }

// scorer accumulates component results.
type scorer struct {
	full   bool
	total  float64
	n      int
	failed bool
	// mismatched names the components that did not match, in evaluation order.
	mismatched []string
}

// add records a component result and reports whether evaluation should continue.
func (s *scorer) add(label string, r Result) bool {
	s.n++
	if !r.Matched {
		s.mismatched = append(s.mismatched, label)
		s.failed = true
		s.total += max(r.Distance, 0.01)
	}
	return s.full || !s.failed
}

func (s *scorer) result() Result {
	if !s.failed {
		return hit()
	}
	return Result{Distance: min(s.total/float64(max(s.n, 1)), 1)}
}

func (m *RequestMatcher) eval(r *Request, full bool) Result {
	s := &scorer{full: full}
	m.evalInto(r, s)
	return s.result()
}

// evalInto evaluates the pattern into s, stopping at the first mismatch unless s.full.
func (m *RequestMatcher) evalInto(r *Request, s *scorer) {
	if m.method != "" && !s.add("Method", boolResult(r.Method == m.method)) {
		return
	}
	ur, params := m.url.match(r)
	if !s.add("URL", ur) {
		return
	}
	if m.scheme != "" && !s.add("Scheme", boolResult(r.Scheme == m.scheme)) {
		return
	}
	if m.port != 0 && !s.add("Port", boolResult(r.Port == m.port)) {
		return
	}
	if m.host != nil && !s.add("Host", m.host.MatchString(r.Host, r.Host != "")) {
		return
	}
	if m.evalParams(r, s, params) && m.evalHeaders(r, s) {
		m.evalBody(r, s)
	}
}

func (m *RequestMatcher) evalParams(r *Request, s *scorer, params map[string]string) bool {
	for _, p := range m.pathParams {
		v, ok := params[p.name]
		if !s.add("Path parameter ["+p.name+"]", p.m.MatchString(v, ok)) {
			return false
		}
	}
	for _, q := range m.query {
		if !s.add("Query parameter ["+q.name+"]", q.m.MatchValues(r.Query[q.name])) {
			return false
		}
	}
	for _, f := range m.form {
		if !s.add("Form parameter ["+f.name+"]", f.m.MatchValues(r.Form()[f.name])) {
			return false
		}
	}
	return true
}

func (m *RequestMatcher) evalHeaders(r *Request, s *scorer) bool {
	for _, h := range m.headers {
		if !s.add("Header ["+h.name+"]", h.m.MatchValues(r.Header.Values(h.name))) {
			return false
		}
	}
	for _, c := range m.cookies {
		vs := r.Cookies[c.name]
		v, ok := "", len(vs) > 0
		if ok {
			v = vs[0]
		}
		if !s.add("Cookie ["+c.name+"]", c.m.MatchString(v, ok)) {
			return false
		}
	}
	if m.basicAuth != "" && !s.add("Basic auth", boolResult(r.Header.Get("Authorization") == m.basicAuth)) {
		return false
	}
	return true
}

func (m *RequestMatcher) evalBody(r *Request, s *scorer) bool {
	if len(m.bodies) > 0 {
		c := Content{Bytes: r.Body, JSON: r.JSON}
		for _, b := range m.bodies {
			if !s.add("Body", b.MatchContent(c)) {
				return false
			}
		}
	}
	if len(m.multipart) > 0 {
		parts := r.parts()
		for _, mp := range m.multipart {
			if !s.add("Multipart body", mp.match(parts)) {
				return false
			}
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

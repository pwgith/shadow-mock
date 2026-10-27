// Package match compiles WireMock request patterns into matchers and evaluates them against
// requests. Compilation happens once per mapping; evaluation never compiles anything (§15).
package match

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/pwgith/shadow-mock/internal/model"
)

// Request is a parsed, read-only view of an HTTP request, built once and shared by every
// layer, matcher, the journal and the detector (DESIGN.md §6.2).
type Request struct {
	Method   string
	Scheme   string
	Host     string
	Port     int
	Path     string // escaped path, after any org and /d/<id> prefixes are stripped
	RawQuery string
	Query    url.Values
	Header   http.Header
	Cookies  map[string][]string
	Body     []byte
	ClientIP string

	jsonOnce sync.Once
	jsonVal  any
	jsonOK   bool

	formOnce sync.Once
	form     url.Values
}

// NewRequest builds a Request from an HTTP request whose body has already been read. path is
// the escaped path to match against, which may differ from r.URL when prefixes were stripped.
func NewRequest(r *http.Request, body []byte, path string) *Request {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host, port := splitHostPort(r.Host, scheme)
	return &Request{
		Method:   r.Method,
		Scheme:   scheme,
		Host:     host,
		Port:     port,
		Path:     path,
		RawQuery: r.URL.RawQuery,
		Query:    r.URL.Query(),
		Header:   r.Header.Clone(),
		Cookies:  cookies(r.Header),
		Body:     body,
		ClientIP: clientIP(r.RemoteAddr),
	}
}

// FromSample builds a Request from a sample request (dry runs and near misses).
func FromSample(s *model.SampleRequest) *Request {
	u, err := url.Parse(s.URL)
	if err != nil {
		u = &url.URL{Path: s.URL}
	}
	h := http.Header{}
	for k, v := range s.Headers {
		h.Set(k, v)
	}
	body := []byte(s.Body)
	if s.BodyAsBase64 != "" {
		if b, err := base64.StdEncoding.DecodeString(s.BodyAsBase64); err == nil {
			body = b
		}
	}
	c := cookies(h)
	for k, v := range s.Cookies {
		c[k] = append(c[k], v)
	}
	host, port := splitHostPort(h.Get("Host"), "http")
	return &Request{
		Method:   strings.ToUpper(s.Method),
		Scheme:   "http",
		Host:     host,
		Port:     port,
		Path:     u.EscapedPath(),
		RawQuery: u.RawQuery,
		Query:    u.Query(),
		Header:   h,
		Cookies:  c,
		Body:     body,
	}
}

// URL returns the path and query, as WireMock's url matcher sees them.
func (r *Request) URL() string {
	if r.RawQuery == "" {
		return r.Path
	}
	return r.Path + "?" + r.RawQuery
}

// ContentType returns the media type of the body, without parameters.
func (r *Request) ContentType() string {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mt
}

// JSON returns the body parsed as JSON, and whether it parsed.
func (r *Request) JSON() (any, bool) {
	r.jsonOnce.Do(func() {
		r.jsonVal, r.jsonOK = parseJSON(r.Body)
	})
	return r.jsonVal, r.jsonOK
}

// Form returns the url-encoded form fields of the body.
func (r *Request) Form() url.Values {
	r.formOnce.Do(func() {
		if r.ContentType() == "application/x-www-form-urlencoded" {
			if v, err := url.ParseQuery(string(r.Body)); err == nil {
				r.form = v
			}
		}
		if r.form == nil {
			r.form = url.Values{}
		}
	})
	return r.form
}

func parseJSON(b []byte) (any, bool) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return nil, false
	}
	return normaliseNumbers(v), true
}

// normaliseNumbers turns json.Number into float64, so values compare and print consistently.
func normaliseNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return t.String()
		}
		return f
	case map[string]any:
		for k, x := range t {
			t[k] = normaliseNumbers(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = normaliseNumbers(x)
		}
		return t
	default:
		return v
	}
}

func cookies(h http.Header) map[string][]string {
	out := map[string][]string{}
	for _, c := range (&http.Request{Header: h}).Cookies() {
		out[c.Name] = append(out[c.Name], c.Value)
	}
	return out
}

func splitHostPort(hostport, scheme string) (string, int) {
	host, p, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
		if scheme == "https" {
			return host, 443
		}
		return host, 80
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		return host, 0
	}
	return host, port
}

func clientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

// WithBody returns a copy of the request that has body in place of its own. The copy shares
// the other fields and starts with no parsed-body caches, so it is safe to keep after the
// original is gone.
func (r *Request) WithBody(body []byte) *Request {
	return &Request{
		Method: r.Method, Scheme: r.Scheme, Host: r.Host, Port: r.Port, Path: r.Path,
		RawQuery: r.RawQuery, Query: r.Query, Header: r.Header, Cookies: r.Cookies,
		Body: body, ClientIP: r.ClientIP,
	}
}

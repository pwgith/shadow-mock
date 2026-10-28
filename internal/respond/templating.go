package respond

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mailgun/raymond/v2"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/respond/template"
)

// ResponseTemplate is the transformer name that turns templating on for one response.
const ResponseTemplate = "response-template"

// Input is what templates may read about the request being answered. A nil Input means
// the response is not templated.
type Input struct {
	// ID identifies the request in the journal; request.id shows it. A new one is made when empty.
	ID         string
	Request    *match.Request
	PathParams map[string]string
	// CallNumber is the stub's call number for this request scope, or 0 when it has none.
	CallNumber int64
}

// templated reports whether def's text is rendered as a template.
func (r *Renderer) templated(def *model.ResponseDefinition, in *Input) bool {
	if in == nil || in.Request == nil {
		return false
	}
	return r.Templating || slices.Contains(def.Transformers, ResponseTemplate)
}

// multiValue is a request value that occurs more than once. It prints as its first value
// and can be indexed, as in request.query.tag.[1].
type multiValue []any

// String implements fmt.Stringer.
func (m multiValue) String() string {
	if len(m) == 0 {
		return ""
	}
	return template.Mark(fmt.Sprint(m[0]))
}

// Plain implements the engine's hook for JSON output: one value is written alone, several as a list.
func (m multiValue) Plain() any {
	out := make([]any, len(m))
	for i, x := range m {
		out[i] = fmt.Sprint(x)
	}
	if len(out) == 1 {
		return out[0]
	}
	return out
}

// pathValue is request.path: it prints as the path, exposes the segments as fields 0, 1 and so on,
// and, for a urlPathTemplate, the path parameters too. The path is held under the empty key.
type pathValue map[string]any

// String implements fmt.Stringer.
func (p pathValue) String() string { return template.Mark(fmt.Sprint(p[""])) }

// Plain implements the engine's hook for JSON output.
func (p pathValue) Plain() any { return fmt.Sprint(p[""]) }

// requestLine is request.requestLine: it prints as the request line, and has the fields of the
// request that WireMock's request line has.
type requestLine map[string]any

// String implements fmt.Stringer.
func (l requestLine) String() string { return template.Mark(fmt.Sprint(l[""])) }

// Plain implements the engine's hook for JSON output.
func (l requestLine) Plain() any { return fmt.Sprint(l[""]) }

// values returns the values of a header, query parameter or cookie. One value prints as itself and
// can still be reached as .first, .last or .[0], as in WireMock.
func values(vs []string) any {
	switch len(vs) {
	case 0:
		return raymond.SafeString("")
	case 1:
		return multiValue{raymond.SafeString(vs[0])}
	default:
		out := make(multiValue, len(vs))
		for i, v := range vs {
			out[i] = raymond.SafeString(v)
		}
		return out
	}
}

// Data builds the root template context for a request: the request model, the original
// request (the same, for webhooks to refer to), the call number and the transformer
// parameters.
func Data(in *Input, params map[string]any) map[string]any {
	req := RequestModel(in)
	return map[string]any{
		"request":         req,
		"originalRequest": req,
		"parameters":      template.Safe(params),
	}
}

// RequestModel is WireMock's request model for templates.
func RequestModel(in *Input) map[string]any {
	r := in.Request
	query := make(map[string]any, len(r.Query))
	for k, vs := range r.Query {
		query[k] = values(vs)
	}
	headers := make(template.Headers, 2*len(r.Header))
	for k, vs := range r.Header {
		v := values(vs)
		headers[k] = v
		headers[strings.ToLower(k)] = v
	}
	cookies := make(map[string]any, len(r.Cookies))
	for k, vs := range r.Cookies {
		cookies[k] = values(vs)
	}
	segments := []any{}
	for _, s := range strings.Split(strings.Trim(r.Path, "/"), "/") {
		if s != "" {
			segments = append(segments, raymond.SafeString(unescape(s)))
		}
	}
	pv := pathValue{"": raymond.SafeString(r.Path)}
	for i, s := range segments {
		pv[strconv.Itoa(i)] = s
	}
	for k, v := range in.PathParams {
		pv[k] = raymond.SafeString(v)
	}
	host := r.Host
	baseURL := r.Scheme + "://" + host
	defaultPort := r.Scheme == "http" && r.Port == 80 || r.Scheme == "https" && r.Port == 443
	if r.Port != 0 && !defaultPort {
		baseURL += ":" + strconv.Itoa(r.Port)
	}
	id := in.ID
	if id == "" {
		id = template.UUID()
	}
	parts, isMultipart := r.Multipart()
	rm := map[string]any{
		"id":           raymond.SafeString(id),
		"url":          raymond.SafeString(r.URL()),
		"absoluteUrl":  raymond.SafeString(baseURL + r.URL()),
		"path":         pv,
		"pathSegments": segments,
		"method":       raymond.SafeString(r.Method),
		"query":        query,
		"headers":      headers,
		"cookies":      cookies,
		"body":         raymond.SafeString(string(r.Body)),
		"bodyAsBase64": raymond.SafeString(base64.StdEncoding.EncodeToString(r.Body)),
		"scheme":       raymond.SafeString(r.Scheme),
		"host":         raymond.SafeString(host),
		"port":         r.Port,
		"baseUrl":      raymond.SafeString(baseURL),
		"clientIp":     raymond.SafeString(r.ClientIP),
		"callNumber":   in.CallNumber,
		"queryString":  raymond.SafeString(r.RawQuery),
		"contentType":  raymond.SafeString(r.Header.Get("Content-Type")),
		"hasBody":      len(r.Body) > 0,
		"multipart":    isMultipart,
		"parts":        multipartParts(parts),
	}
	line := requestLine{"": raymond.SafeString(r.Method + " " + r.URL() + " HTTP/1.1")}
	for _, k := range []string{"method", "path", "url", "host", "port", "scheme", "baseUrl", "query", "pathSegments"} {
		line[k] = rm[k]
	}
	rm["requestLine"] = line
	return rm
}

// multipartParts is request.parts: each part of a multipart body by its name, with its body, its
// body as base64, whether it is binary, and its headers (the first part wins when names repeat).
func multipartParts(parts []match.MultipartPart) map[string]any {
	out := make(map[string]any, len(parts))
	for _, p := range parts {
		_, params, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := params["name"]
		if _, dup := out[name]; err != nil || dup {
			continue
		}
		headers := make(template.Headers, 2*len(p.Header))
		for k, vs := range p.Header {
			headers[k] = values(vs)
			headers[strings.ToLower(k)] = values(vs)
		}
		out[name] = map[string]any{
			"name":         raymond.SafeString(name),
			"body":         raymond.SafeString(string(p.Body)),
			"bodyAsBase64": raymond.SafeString(base64.StdEncoding.EncodeToString(p.Body)),
			"binary":       isBinaryType(p.Header.Get("Content-Type")),
			"headers":      headers,
		}
	}
	return out
}

// isBinaryType reports whether a part's content type is not text. A part with no type is binary.
func isBinaryType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true
	}
	for _, text := range []string{"text/", "json", "xml", "javascript", "yaml", "x-www-form-urlencoded"} {
		if strings.HasPrefix(mt, text) || !strings.HasSuffix(text, "/") && strings.Contains(mt, text) {
			return false
		}
	}
	return true
}

func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// renderHeaders renders each header value as a template.
func (r *Renderer) renderHeaders(h http.Header, data map[string]any) error {
	for k, vs := range h {
		for i, v := range vs {
			out, err := r.Templates.Render(v, data)
			if err != nil {
				return fmt.Errorf("header %s: %w", k, err)
			}
			vs[i] = out
		}
	}
	return nil
}

// First returns the first value, for request.query.tag.first.
func (m multiValue) First() any {
	if len(m) == 0 {
		return raymond.SafeString("")
	}
	return m[0]
}

// Last returns the last value, for request.query.tag.last.
func (m multiValue) Last() any {
	if len(m) == 0 {
		return raymond.SafeString("")
	}
	return m[len(m)-1]
}

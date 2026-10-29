package record

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pwgith/shadow-mock/internal/ids"
	"github.com/pwgith/shadow-mock/internal/model"
)

// equalToKey is the name of the equality matcher in a recorded pattern.
const equalToKey = "equalTo"

// transferHeader reports whether a response header describes the transfer, not the content,
// so it is not recorded.
func transferHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Content-Length", "Transfer-Encoding", "Content-Encoding", "Connection":
		return true
	}
	return false
}

// File is a response body kept out of its mapping, to be stored in __files.
type File struct {
	Name string
	Body []byte
}

// Build turns exchanges into mappings: one per distinct request, in the order first seen.
// A repeat of a request is dropped and the mapping answers with the last response seen, or
// with RepeatsAsCallResponses the repeats become one mapping whose callResponses give each
// call its recorded response. Bodies that the spec's extractBodyCriteria sizes pick out are
// returned as files, which the mappings name with bodyFileName.
func Build(spec *Spec, exchanges []Exchange) ([]model.StubMapping, []File, error) {
	var order []string
	groups := map[string]*group{}
	for i := range exchanges {
		e := &exchanges[i]
		req := requestPattern(spec, e)
		key, err := json.Marshal(req)
		if err != nil {
			return nil, nil, fmt.Errorf("encode recorded request: %w", err)
		}
		g, ok := groups[string(key)]
		if !ok {
			g = &group{request: req, name: nameOf(e.Request.Path)}
			groups[string(key)] = g
			order = append(order, string(key))
		}
		g.replies = append(g.replies, e)
	}
	out := make([]model.StubMapping, 0, len(order))
	var files []File
	for _, k := range order {
		m, fs, err := groups[k].mapping(spec)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, m)
		files = append(files, fs...)
	}
	return out, files, nil
}

// group is the exchanges that share one request pattern.
type group struct {
	request map[string]any
	name    string
	replies []*Exchange
}

// mapping builds the mapping for a group, and the files its response bodies are kept in.
func (g *group) mapping(spec *Spec) (model.StubMapping, []File, error) {
	id := ids.NewUUID()
	var files []File
	reply := func(e *Exchange) map[string]any {
		def, f := responseDefinition(spec, e, g.name, id)
		if f != nil {
			files = append(files, *f)
		}
		return def
	}
	doc := map[string]any{"id": id, "request": g.request, "response": reply(g.replies[len(g.replies)-1])}
	if g.name != "" && !spec.live {
		doc["name"] = g.name
	}
	if spec.RepeatsAsCallResponses && len(g.replies) > 1 {
		rules := make([]map[string]any, len(g.replies))
		for i, e := range g.replies {
			rules[i] = map[string]any{"calls": strconv.Itoa(i + 1), "response": reply(e)}
		}
		doc["callResponses"] = rules // later calls repeat the last response
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return model.StubMapping{}, nil, fmt.Errorf("encode recorded mapping: %w", err)
	}
	m, err := model.DecodeStubMapping(b)
	if err != nil {
		return model.StubMapping{}, nil, fmt.Errorf("recorded mapping is not valid: %w", err)
	}
	return m, files, nil
}

// nameOf turns a request path into a mapping name the way WireMock does: no leading slash,
// slashes as underscores, anything but letters, digits, dot, hyphen and underscore as a hyphen.
func nameOf(escapedPath string) string {
	p, err := url.PathUnescape(escapedPath)
	if err != nil {
		p = escapedPath
	}
	var b strings.Builder
	for _, r := range strings.TrimPrefix(p, "/") {
		switch {
		case r == '/':
			b.WriteByte('_')
		case r == '.' || r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func requestPattern(spec *Spec, e *Exchange) map[string]any {
	req := map[string]any{"method": e.Request.Method}
	if len(e.Request.Query) == 0 || spec.live {
		req["url"] = e.Request.URL()
	} else {
		req["urlPath"] = e.Request.Path
		params := map[string]any{}
		for name, vs := range e.Request.Query {
			exact := make([]any, len(vs))
			for i, v := range vs {
				exact[i] = map[string]any{equalToKey: v}
			}
			params[name] = map[string]any{"hasExactly": exact}
		}
		req["queryParameters"] = params
	}
	if len(spec.CaptureHeaders) > 0 {
		headers := map[string]any{}
		for name, c := range spec.CaptureHeaders {
			v := e.Request.Header.Get(name)
			if v == "" {
				continue
			}
			headers[name] = map[string]any{equalToKey: v, "caseInsensitive": c.CaseInsensitive}
		}
		if len(headers) > 0 {
			req["headers"] = headers
		}
	}
	if len(e.Request.Body) > 0 {
		if p := bodyPattern(spec.RequestBodyPattern, e); p != nil {
			req["bodyPatterns"] = []any{p}
		}
	}
	return req
}

// bodyPattern turns a request body into a pattern. Without a requestBodyPattern, or with
// matcher "auto", the pattern follows the content type.
func bodyPattern(bp *BodyPattern, e *Exchange) map[string]any {
	if bp == nil {
		bp = &BodyPattern{IgnoreArrayOrder: true, IgnoreExtraElements: true} // WireMock's default
	}
	body := string(e.Request.Body)
	kind := bp.Matcher
	if kind == "" || kind == "auto" {
		switch ct := e.Request.ContentType(); {
		case strings.HasSuffix(ct, "json"):
			kind = "equalToJson"
		case strings.HasSuffix(ct, "xml"):
			kind = "equalToXml"
		default:
			kind = equalToKey
		}
	}
	p := map[string]any{kind: body}
	switch kind {
	case "equalToJson":
		p["ignoreArrayOrder"] = bp.IgnoreArrayOrder
		p["ignoreExtraElements"] = bp.IgnoreExtraElements
	case equalToKey:
		p["caseInsensitive"] = bp.CaseInsensitive
	}
	return p
}

// responseDefinition describes an exchange's response. When the body is big enough to be extracted
// it goes to the returned file, named after the mapping, and the definition points to it.
func responseDefinition(spec *Spec, e *Exchange, name, id string) (map[string]any, *File) {
	def := map[string]any{"status": e.Status}
	headers := map[string]any{}
	for k, vs := range e.Header {
		if transferHeader(k) || len(vs) == 0 {
			continue
		}
		if len(vs) == 1 {
			headers[k] = vs[0]
		} else {
			headers[k] = vs
		}
	}
	if len(headers) > 0 {
		def["headers"] = headers
	}
	text := isText(e)
	if len(e.Body) > 0 && spec.ExtractBodyCriteria.extracts(len(e.Body), !text) {
		prefix := name
		if spec.live {
			prefix = "body-" + name // what WireMock calls the files of --record-mappings
		}
		f := &File{Name: prefix + "-" + id + extensionOf(e), Body: e.Body}
		def["bodyFileName"] = f.Name
		return def, f
	}
	switch {
	case len(e.Body) == 0:
	case text:
		def["body"] = string(e.Body)
	default:
		def["base64Body"] = base64.StdEncoding.EncodeToString(e.Body)
	}
	return def, nil
}

// extensionOf picks the extension of a body file from the content type.
func extensionOf(e *Exchange) string {
	mt := mediaType(e)
	switch {
	case strings.HasSuffix(mt, "json"):
		return ".json"
	case strings.HasSuffix(mt, "xml"):
		return ".xml"
	case mt == "text/html":
		return ".html"
	}
	return ".txt"
}

func mediaType(e *Exchange) string {
	mt, _, err := mime.ParseMediaType(e.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mt
}

func isText(e *Exchange) bool {
	mt := mediaType(e)
	textual := mt == "" || strings.HasPrefix(mt, "text/") || strings.HasSuffix(mt, "xml") ||
		strings.HasSuffix(mt, "json") || strings.HasSuffix(mt, "javascript") || mt == "application/x-www-form-urlencoded"
	return textual && utf8.Valid(e.Body)
}

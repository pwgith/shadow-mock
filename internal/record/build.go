package record

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pwgith/shadow-mock/internal/ids"
	"github.com/pwgith/shadow-mock/internal/model"
)

// transferHeader reports whether a response header describes the transfer, not the content,
// so it is not recorded.
func transferHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Content-Length", "Transfer-Encoding", "Content-Encoding", "Connection", "Keep-Alive", "Date":
		return true
	}
	return false
}

// Build turns exchanges into mappings: one per distinct request, in the order first seen.
// Repeats of a request are dropped, or with RepeatsAsCallResponses become one mapping whose
// callResponses give each call its recorded response.
func Build(spec *Spec, exchanges []Exchange) ([]model.StubMapping, error) {
	type group struct {
		request map[string]any
		replies []map[string]any
	}
	var order []string
	groups := map[string]*group{}
	for i := range exchanges {
		e := &exchanges[i]
		req := requestPattern(spec, e)
		key, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("encode recorded request: %w", err)
		}
		g, ok := groups[string(key)]
		if !ok {
			g = &group{request: req}
			groups[string(key)] = g
			order = append(order, string(key))
		}
		g.replies = append(g.replies, responseDefinition(e))
	}
	out := make([]model.StubMapping, 0, len(order))
	for _, k := range order {
		g := groups[k]
		doc := map[string]any{"id": ids.NewUUID(), "request": g.request, "response": g.replies[0]}
		if spec.RepeatsAsCallResponses && len(g.replies) > 1 {
			rules := make([]map[string]any, len(g.replies))
			for i, r := range g.replies {
				rules[i] = map[string]any{"calls": strconv.Itoa(i + 1), "response": r}
			}
			doc["callResponses"] = rules
			doc["response"] = g.replies[len(g.replies)-1] // later calls repeat the last response
		}
		b, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("encode recorded mapping: %w", err)
		}
		m, err := model.DecodeStubMapping(b)
		if err != nil {
			return nil, fmt.Errorf("recorded mapping is not valid: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}

func requestPattern(spec *Spec, e *Exchange) map[string]any {
	req := map[string]any{"method": e.Request.Method, "url": e.Request.URL()}
	if len(spec.CaptureHeaders) > 0 {
		headers := map[string]any{}
		for name, c := range spec.CaptureHeaders {
			v := e.Request.Header.Get(name)
			if v == "" {
				continue
			}
			p := map[string]any{"equalTo": v}
			if c.CaseInsensitive {
				p["caseInsensitive"] = true
			}
			headers[name] = p
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
		return nil
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
			kind = "equalTo"
		}
	}
	p := map[string]any{kind: body}
	switch kind {
	case "equalToJson":
		p["ignoreArrayOrder"] = bp.IgnoreArrayOrder
		p["ignoreExtraElements"] = bp.IgnoreExtraElements
	case "equalTo":
		if bp.CaseInsensitive {
			p["caseInsensitive"] = true
		}
	}
	return p
}

func responseDefinition(e *Exchange) map[string]any {
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
	switch {
	case len(e.Body) == 0:
	case isJSON(e):
		def["jsonBody"] = json.RawMessage(e.Body)
	case isText(e):
		def["body"] = string(e.Body)
	default:
		def["base64Body"] = base64.StdEncoding.EncodeToString(e.Body)
	}
	return def
}

func mediaType(e *Exchange) string {
	mt, _, err := mime.ParseMediaType(e.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mt
}

func isJSON(e *Exchange) bool {
	return strings.HasSuffix(mediaType(e), "json") && json.Valid(e.Body)
}

func isText(e *Exchange) bool {
	mt := mediaType(e)
	textual := mt == "" || strings.HasPrefix(mt, "text/") || strings.HasSuffix(mt, "xml") ||
		strings.HasSuffix(mt, "json") || strings.HasSuffix(mt, "javascript") || mt == "application/x-www-form-urlencoded"
	return textual && utf8.Valid(e.Body)
}

package match

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/pwgith/shadow-mock/internal/model"
)

// multipartMatcher implements multipartPatterns (FR-2.5).
type multipartMatcher struct {
	all     bool
	headers map[string]MultiMatcher
	bodies  []ContentMatcher
}

type part struct {
	header textproto.MIMEHeader
	body   []byte
}

func compileMultipart(p model.MultipartPattern, o Options) (*multipartMatcher, error) {
	m := &multipartMatcher{all: strings.EqualFold(p.MatchingType, "ALL"), headers: map[string]MultiMatcher{}}
	for name, vp := range p.Headers {
		mm, err := CompileMulti(vp, o)
		if err != nil {
			return nil, fmt.Errorf("multipart header %s: %w", name, err)
		}
		m.headers[name] = mm
	}
	for i, bp := range p.BodyPatterns {
		cm, err := CompileContent(bp, o)
		if err != nil {
			return nil, fmt.Errorf("multipart body pattern %d: %w", i, err)
		}
		m.bodies = append(m.bodies, cm)
	}
	return m, nil
}

func (m *multipartMatcher) match(parts []part) Result {
	if len(parts) == 0 {
		return miss()
	}
	for _, p := range parts {
		ok := m.matchPart(p)
		if ok && !m.all {
			return hit()
		}
		if !ok && m.all {
			return miss()
		}
	}
	return boolResult(m.all)
}

func (m *multipartMatcher) matchPart(p part) bool {
	for name, mm := range m.headers {
		if !mm.MatchValues(p.header.Values(name)).Matched {
			return false
		}
	}
	for _, cm := range m.bodies {
		if !cm.MatchContent(ContentOf(p.body)).Matched {
			return false
		}
	}
	return true
}

// parts parses a multipart body. It returns nil if the body is not multipart.
func (r *Request) parts() []part {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mt, "multipart/") || params["boundary"] == "" {
		return nil
	}
	mr := multipart.NewReader(bytes.NewReader(r.Body), params["boundary"])
	var out []part
	for {
		p, err := mr.NextPart()
		if err != nil {
			return out
		}
		b, err := io.ReadAll(p)
		if err != nil {
			return out
		}
		out = append(out, part{header: p.Header, body: b})
	}
}

// MultipartPart is one part of a multipart request body.
type MultipartPart struct {
	Header textproto.MIMEHeader
	Body   []byte
}

// Multipart returns the parts of a multipart body, and whether the request is multipart at all.
func (r *Request) Multipart() ([]MultipartPart, bool) {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mt, "multipart/") || params["boundary"] == "" {
		return nil, false
	}
	var out []MultipartPart
	for _, p := range r.parts() {
		out = append(out, MultipartPart{Header: p.header, Body: p.body})
	}
	return out, true
}

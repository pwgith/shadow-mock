// Package proxy forwards a request to a real server and returns its response (FR-3.5). It is
// used by mappings with proxyBaseUrl and by recording, which captures what comes back.
package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pwgith/shadow-mock/internal/egress"
	"github.com/pwgith/shadow-mock/internal/match"
)

// maxResponseBytes caps the upstream body that is held in memory.
const maxResponseBytes = 64 << 20

// Error codes the service reports for failed forwards.
const (
	CodeBlocked    = "egress_blocked"
	CodeDenied     = "target_denied"
	CodeBadGateway = "bad_gateway"
)

// Error is a forward that failed. Code is one of the Code constants.
type Error struct {
	Code string
	Err  error
}

// Error implements error.
func (e *Error) Error() string { return e.Err.Error() }

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

// Options are the per-response proxy settings of a mapping (WireMock's proxy response fields).
type Options struct {
	// AdditionalHeaders are added to the forwarded request.
	AdditionalHeaders map[string]string
	// RemoveHeaders are dropped from the forwarded request.
	RemoveHeaders []string
	// PrefixToRemove is cut from the start of the path before forwarding.
	PrefixToRemove string
}

// Result is the upstream response.
type Result struct {
	Status int
	Header http.Header
	Body   []byte
}

// Forwarder sends requests upstream through an egress client.
type Forwarder struct {
	client *http.Client
	// Targets, when set, refuses upstream hosts its rules do not allow.
	Targets *egress.Targets
	// PreserveHost sends the client's Host header upstream instead of the target's.
	PreserveHost bool
	// Inject, when set, adds the caller's trace context to the upstream request, so a proxied call
	// continues the trace.
	Inject func(ctx context.Context, h http.Header)
}

// New returns a forwarder that sends through client.
func New(client *http.Client) *Forwarder { return &Forwarder{client: client} }

// hopByHop headers describe one connection and are never forwarded, in either direction.
func hopByHop() []string {
	return []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade",
	}
}

// Forward sends req to base and returns the response.
func (f *Forwarder) Forward(ctx context.Context, req *match.Request, base string, o Options) (*Result, error) {
	target, err := targetURL(base, req, o.PrefixToRemove)
	if err != nil {
		return nil, &Error{Code: CodeBadGateway, Err: err}
	}
	out, err := http.NewRequestWithContext(ctx, req.Method, target, bytes.NewReader(req.Body))
	if err != nil {
		return nil, &Error{Code: CodeBadGateway, Err: fmt.Errorf("build upstream request: %w", err)}
	}
	if denied := f.Targets.Check(ctx, out.URL.Hostname()); denied != nil {
		return nil, &Error{Code: CodeDenied, Err: denied}
	}
	if host := req.Header.Get("Host"); f.PreserveHost && host != "" {
		out.Host = host
	}
	for k, vs := range req.Header {
		out.Header[k] = append([]string(nil), vs...)
	}
	for _, h := range hopByHop() {
		out.Header.Del(h)
	}
	// The upstream answers uncompressed, so the body can be recorded and served as it is.
	out.Header.Del("Accept-Encoding")
	out.Header.Del("Content-Length")
	for _, h := range o.RemoveHeaders {
		out.Header.Del(h)
	}
	for k, v := range o.AdditionalHeaders {
		out.Header.Set(k, v)
	}
	if f.Inject != nil {
		f.Inject(ctx, out.Header)
	}
	resp, err := f.client.Do(out)
	if err != nil {
		code := CodeBadGateway
		if errors.Is(err, egress.ErrBlocked) {
			code = CodeBlocked
		}
		return nil, &Error{Code: code, Err: fmt.Errorf("proxy to %s: %w", target, err)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &Error{Code: CodeBadGateway, Err: fmt.Errorf("read upstream response: %w", err)}
	}
	if len(body) > maxResponseBytes {
		return nil, &Error{Code: CodeBadGateway, Err: fmt.Errorf("upstream response exceeds %d bytes", maxResponseBytes)}
	}
	h := resp.Header.Clone()
	for _, name := range hopByHop() {
		h.Del(name)
	}
	h.Del("Content-Length")
	return &Result{Status: resp.StatusCode, Header: h, Body: body}, nil
}

// targetURL joins the upstream base URL with the request's path and query.
func targetURL(base string, req *match.Request, prefix string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("proxyBaseUrl %q is not an absolute URL", base)
	}
	path := req.Path
	if prefix != "" {
		path = strings.TrimPrefix(path, prefix)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	target := strings.TrimRight(base, "/") + path
	if req.RawQuery != "" {
		target += "?" + req.RawQuery
	}
	return target, nil
}

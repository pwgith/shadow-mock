package contract

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// ValidatingTransport checks every request and response it carries against the contract.
// Violations are collected rather than returned, so callers keep the real response and can
// assert on both. It is safe for concurrent use.
type ValidatingTransport struct {
	Spec *Spec
	// Base is the engine base URL. Its path (for example /acme in SaaS) is removed before
	// operations are matched.
	Base *url.URL
	// Next performs the request. Defaults to http.DefaultTransport.
	Next http.RoundTripper

	mu         sync.Mutex
	violations []string
}

// RoundTrip implements http.RoundTripper.
func (t *ValidatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		_ = req.Body.Close()
		reqBody = b
		req.Body = io.NopCloser(bytes.NewReader(b))
	}

	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}

	respBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	path := req.URL.Path
	if t.Base != nil {
		path = strings.TrimPrefix(path, strings.TrimSuffix(t.Base.Path, "/"))
	}
	if req.URL.RawQuery != "" {
		path += "?" + req.URL.RawQuery
	}
	found := t.Spec.Check(Exchange{
		Method:       req.Method,
		Path:         path,
		RequestType:  req.Header.Get("Content-Type"),
		RequestBody:  reqBody,
		Status:       resp.StatusCode,
		ResponseType: resp.Header.Get("Content-Type"),
		ResponseBody: respBody,
	})
	if len(found) > 0 {
		t.mu.Lock()
		t.violations = append(t.violations, found...)
		t.mu.Unlock()
	}
	return resp, nil
}

// TakeViolations returns the violations recorded so far and clears them.
func (t *ValidatingTransport) TakeViolations() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := t.violations
	t.violations = nil
	return v
}

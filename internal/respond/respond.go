// Package respond renders response definitions: body sources, headers, delays, gzip and
// faults, and templating (DESIGN.md §9, FR-3.1–FR-3.5a, FR-3.4). Proxying lives in internal/proxy.
package respond

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/respond/template"
	"github.com/pwgith/shadow-mock/internal/store"
)

// Settings are WireMock's global settings that affect responses.
type Settings struct {
	FixedDelay        int64                    `json:"fixedDelay,omitempty"`
	DelayDistribution *model.DelayDistribution `json:"delayDistribution,omitempty"`
	ProxyPassThrough  bool                     `json:"proxyPassThrough,omitempty"`
	Extended          map[string]any           `json:"extended,omitempty"`
}

// Renderer turns response definitions into responses.
type Renderer struct {
	Files store.FileRepository
	Clock clock.Clock
	// Gzip enables compression for clients that accept it (WireMock's default).
	Gzip bool
	// Templating renders every response as a template, not only those that ask for it.
	Templating bool
	// Templates renders response text; NewRenderer sets it.
	Templates *template.Engine

	mu       sync.RWMutex
	settings Settings
}

// NewRenderer returns a renderer.
func NewRenderer(files store.FileRepository, c clock.Clock, gzip bool) *Renderer {
	return &Renderer{Files: files, Clock: c, Gzip: gzip, Templates: template.New(c)}
}

// Settings returns the global settings.
func (r *Renderer) Settings() Settings {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.settings
}

// SetSettings replaces the global settings.
func (r *Renderer) SetSettings(s Settings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings = s
}

// Response is a rendered response, ready to write.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
	Fault  string
	Delay  time.Duration
	// Dribble sends the body in chunks over a duration.
	Dribble      *model.ChunkedDribbleDelay
	GzipDisabled bool
}

// ErrProxyInDryRun is returned when a proxy response is rendered without a request to
// forward, as in a dry run.
var ErrProxyInDryRun = errors.New("a dry run does not call the upstream server")

// Render builds the response for def. files lists the scopes to look up bodyFileName in,
// most specific first, so a session's files override global ones.
func (r *Renderer) Render(ctx context.Context, def *model.ResponseDefinition, files []store.Scope, in *Input) (*Response, error) {
	if def.ProxyBaseURL != "" {
		return nil, apierr.New(http.StatusNotImplemented, apierr.NotSupported, "%v (proxyBaseUrl %s).", ErrProxyInDryRun, def.ProxyBaseURL)
	}
	var data map[string]any
	if r.templated(def, in) {
		data = Data(in, def.TransformerParameters)
	}
	out := &Response{
		Status:       def.StatusOrDefault(),
		Header:       http.Header{},
		Fault:        def.Fault,
		Delay:        r.delay(def),
		Dribble:      def.ChunkedDribbleDelay,
		GzipDisabled: def.GzipDisabled,
	}
	for k, vs := range def.Headers {
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}
	if data != nil {
		if err := r.renderHeaders(out.Header, data); err != nil {
			return nil, templateError(err)
		}
	}
	body, err := r.body(ctx, def, files, data)
	if err != nil {
		return nil, err
	}
	out.Body = body
	if len(def.JSONBody) > 0 && out.Header.Get("Content-Type") == "" {
		out.Header.Set("Content-Type", "application/json")
	}
	return out, nil
}

// body builds the response body. A non-nil data means the text is rendered as a template.
func (r *Renderer) body(ctx context.Context, def *model.ResponseDefinition, files []store.Scope, data map[string]any) ([]byte, error) {
	render := func(s string) ([]byte, error) {
		if data == nil {
			return []byte(s), nil
		}
		out, err := r.Templates.Render(s, data)
		if err != nil {
			return nil, templateError(err)
		}
		return []byte(out), nil
	}
	switch {
	case def.Body != nil:
		return render(*def.Body)
	case len(def.JSONBody) > 0:
		return render(string(def.JSONBody))
	case def.Base64Body != "":
		b, err := base64.StdEncoding.DecodeString(def.Base64Body)
		if err != nil {
			return nil, fmt.Errorf("decode base64Body: %w", err)
		}
		return b, nil
	case def.BodyFileName != "":
		name := def.BodyFileName
		if data != nil {
			n, err := r.Templates.Render(name, data)
			if err != nil {
				return nil, templateError(err)
			}
			name = n
		}
		b, err := r.readFile(ctx, files, name)
		if err != nil {
			return nil, err
		}
		return render(string(b))
	default:
		return nil, nil
	}
}

// readFile reads a body file from the first scope that has it.
func (r *Renderer) readFile(ctx context.Context, files []store.Scope, name string) ([]byte, error) {
	for _, s := range files {
		b, err := r.Files.Read(ctx, s, name)
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("read body file %s: %w", name, err)
		}
	}
	return nil, apierr.New(http.StatusInternalServerError, apierr.NotFound, "Body file %s was not found.", name)
}

// templateError reports a template that does not parse or render as a 500.
func templateError(err error) error {
	return apierr.New(http.StatusInternalServerError, "template_error", "The response template failed: %v.", err)
}

// delay combines the response's own delays with the global settings (FR-3.2).
func (r *Renderer) delay(def *model.ResponseDefinition) time.Duration {
	s := r.Settings()
	ms := def.FixedDelayMilliseconds + s.FixedDelay
	if def.DelayDistribution != nil {
		ms += sample(def.DelayDistribution)
	} else if s.DelayDistribution != nil {
		ms += sample(s.DelayDistribution)
	}
	return time.Duration(ms) * time.Millisecond
}

// SampleDelay draws a delay from a distribution.
func SampleDelay(d *model.DelayDistribution) time.Duration {
	return time.Duration(sample(d)) * time.Millisecond
}

// sample draws a delay in milliseconds. math/rand is fine here: delays are not secrets.
func sample(d *model.DelayDistribution) int64 {
	switch d.Type {
	case "fixed":
		return d.Milliseconds
	case "uniform":
		if d.Upper <= d.Lower {
			return d.Lower
		}
		return d.Lower + rand.Int64N(d.Upper-d.Lower+1) //nolint:gosec // G404: delay jitter, not a secret
	case "lognormal":
		v := int64(d.Median * math.Exp(rand.NormFloat64()*d.Sigma)) //nolint:gosec // G404: delay jitter, not a secret
		if d.MaxValue > 0 && v > d.MaxValue {
			return d.MaxValue
		}
		return v
	default:
		return 0
	}
}

// FromUpstream wraps a response received from a proxied server. The mapping's delays and
// gzip setting still apply to it (FR-3.5).
func (r *Renderer) FromUpstream(def *model.ResponseDefinition, status int, header http.Header, body []byte) *Response {
	return &Response{
		Status:       status,
		Header:       header,
		Body:         body,
		Delay:        r.delay(def),
		Dribble:      def.ChunkedDribbleDelay,
		GzipDisabled: def.GzipDisabled,
	}
}

// RenderText renders s as a template when def is templated, as WireMock does for a proxy
// target. Otherwise s is returned as it is.
func (r *Renderer) RenderText(def *model.ResponseDefinition, in *Input, s string) (string, error) {
	if !r.templated(def, in) {
		return s, nil
	}
	out, err := r.Templates.Render(s, Data(in, def.TransformerParameters))
	if err != nil {
		return "", templateError(err)
	}
	return out, nil
}

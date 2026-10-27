// Package respond renders response definitions: body sources, headers, delays, gzip and
// faults (DESIGN.md §9, FR-3.1–FR-3.5a). Templating and proxying arrive in P3.
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

	mu       sync.RWMutex
	settings Settings
}

// NewRenderer returns a renderer.
func NewRenderer(files store.FileRepository, c clock.Clock, gzip bool) *Renderer {
	return &Renderer{Files: files, Clock: c, Gzip: gzip}
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

// ErrProxyNotImplemented is returned for proxy responses until P3.
var ErrProxyNotImplemented = errors.New("proxying is not implemented yet")

// Render builds the response for def. files lists the scopes to look up bodyFileName in,
// most specific first, so a session's files override global ones.
func (r *Renderer) Render(ctx context.Context, def *model.ResponseDefinition, files []store.Scope) (*Response, error) {
	if def.ProxyBaseURL != "" {
		return nil, apierr.New(http.StatusNotImplemented, apierr.NotSupported, "%v (proxyBaseUrl %s).", ErrProxyNotImplemented, def.ProxyBaseURL)
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
	body, err := r.body(ctx, def, files)
	if err != nil {
		return nil, err
	}
	out.Body = body
	if len(def.JSONBody) > 0 && out.Header.Get("Content-Type") == "" {
		out.Header.Set("Content-Type", "application/json")
	}
	return out, nil
}

func (r *Renderer) body(ctx context.Context, def *model.ResponseDefinition, files []store.Scope) ([]byte, error) {
	switch {
	case def.Body != nil:
		return []byte(*def.Body), nil
	case len(def.JSONBody) > 0:
		return def.JSONBody, nil
	case def.Base64Body != "":
		b, err := base64.StdEncoding.DecodeString(def.Base64Body)
		if err != nil {
			return nil, fmt.Errorf("decode base64Body: %w", err)
		}
		return b, nil
	case def.BodyFileName != "":
		for _, s := range files {
			b, err := r.Files.Read(ctx, s, def.BodyFileName)
			if err == nil {
				return b, nil
			}
			if !errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("read body file %s: %w", def.BodyFileName, err)
			}
		}
		return nil, apierr.New(http.StatusInternalServerError, apierr.NotFound, "Body file %s was not found.", def.BodyFileName)
	default:
		return nil, nil
	}
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

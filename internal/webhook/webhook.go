// Package webhook makes the outbound calls a mapping asks for after its response is served
// (FR-6.2). The calls run in the background and never change the served response.
package webhook

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"sync"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/egress"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/respond/template"
)

// maxInFlight bounds webhooks running at once; further ones are dropped and logged.
const maxInFlight = 256

// Dispatcher sends webhooks.
type Dispatcher struct {
	client    *http.Client
	templates *template.Engine
	clock     clock.Clock
	logger    *slog.Logger

	ctx    context.Context //nolint:containedctx // the dispatcher's lifetime, cancelled by Close
	cancel context.CancelFunc
	wg     sync.WaitGroup
	slots  chan struct{}
}

// New returns a dispatcher that sends through client.
func New(client *http.Client, templates *template.Engine, c clock.Clock, logger *slog.Logger) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		client: client, templates: templates, clock: c, logger: logger,
		ctx: ctx, cancel: cancel, slots: make(chan struct{}, maxInFlight),
	}
}

// Close cancels webhooks that are waiting or in flight and waits for them to stop.
func (d *Dispatcher) Close() {
	d.cancel()
	d.wg.Wait()
}

// Fire starts the hooks for a request that has been answered with resp. It returns at once.
func (d *Dispatcher) Fire(hooks []model.Webhook, in *respond.Input, params map[string]any, resp *respond.Response) {
	if len(hooks) == 0 || in == nil {
		return
	}
	data := respond.Data(in, params) // copied per hook below: assigning helpers write to it
	data["response"] = template.Safe(map[string]any{
		"status":  resp.Status,
		"body":    string(resp.Body),
		"headers": headerMap(resp.Header),
	})
	for i := range hooks {
		select {
		case d.slots <- struct{}{}:
		default:
			d.logger.Warn("webhook dropped: too many in flight", "url", hooks[i].URL)
			continue
		}
		d.wg.Add(1)
		go func(h model.Webhook) {
			defer d.wg.Done()
			defer func() { <-d.slots }()
			if err := d.send(h, data); err != nil {
				d.report(h, err)
			}
		}(hooks[i])
	}
}

func headerMap(h http.Header) map[string]any {
	out := make(map[string]any, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

func (d *Dispatcher) report(h model.Webhook, err error) {
	switch {
	case errors.Is(err, context.Canceled):
	case errors.Is(err, egress.ErrBlocked):
		d.logger.Warn("egress_blocked", "kind", "webhook", "url", h.URL, "error", err.Error())
	default:
		d.logger.Warn("webhook failed", "url", h.URL, "error", err.Error())
	}
}

// send waits out the hook's delay, renders its templates and makes the call.
func (d *Dispatcher) send(h model.Webhook, data map[string]any) error {
	data = maps.Clone(data) // assigning helpers write to the data, and hooks run concurrently
	if h.Delay != nil && !d.clock.Sleep(d.ctx, respond.SampleDelay(h.Delay)) {
		return d.ctx.Err()
	}
	url, err := d.templates.Render(h.URL, data)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}
	body, err := d.body(h, data)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(d.ctx, h.Method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	for k, vs := range h.Headers {
		for _, v := range vs {
			out, rerr := d.templates.Render(v, data)
			if rerr != nil {
				return fmt.Errorf("header %s: %w", k, rerr)
			}
			req.Header.Add(k, out)
		}
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) //nolint:errcheck // the reply is not used
	if resp.StatusCode >= http.StatusBadRequest {
		d.logger.Debug("webhook target answered with an error", "url", url, "status", resp.StatusCode)
	}
	return nil
}

func (d *Dispatcher) body(h model.Webhook, data map[string]any) ([]byte, error) {
	if h.Base64Body != "" {
		b, err := base64.StdEncoding.DecodeString(h.Base64Body)
		if err != nil {
			return nil, fmt.Errorf("base64Body: %w", err)
		}
		return b, nil
	}
	out, err := d.templates.Render(h.Body, data)
	if err != nil {
		return nil, fmt.Errorf("body: %w", err)
	}
	return []byte(out), nil
}

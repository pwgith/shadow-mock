// Package telemetry owns OpenTelemetry tracing and the log handler that ties log lines to traces
// (NFR-6.3). Tracing is off unless configured, and everything here is safe to use when it is off.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Config says where traces go.
type Config struct {
	Enabled bool
	// Endpoint is the OTLP/HTTP collector, such as http://localhost:4318. Empty uses the
	// OpenTelemetry environment variables, or the collector's default address.
	Endpoint    string
	ServiceName string
	// SampleRatio is the share of new traces kept, 0 to 1. A trace that arrives already sampled by
	// the caller is kept whatever the ratio says.
	SampleRatio float64
}

// Telemetry creates spans and carries trace context in and out of the server.
type Telemetry struct {
	tracer   trace.Tracer
	prop     propagation.TextMapPropagator
	shutdown func(context.Context) error
}

// Disabled is a Telemetry that records nothing. It still reads a caller's trace context, so log
// lines carry the caller's trace id.
func Disabled() *Telemetry {
	return &Telemetry{tracer: noop.NewTracerProvider().Tracer("shadowmock"), prop: propagation.TraceContext{}}
}

// New starts tracing. A non-nil provider is used as it is (tests give one that keeps spans in
// memory), and the caller owns it. Otherwise an OTLP/HTTP exporter is built from cfg.
func New(ctx context.Context, cfg Config, version string, provider trace.TracerProvider) (*Telemetry, error) {
	if provider != nil {
		return &Telemetry{tracer: provider.Tracer("shadowmock"), prop: propagation.TraceContext{}}, nil
	}
	if !cfg.Enabled {
		return Disabled(), nil
	}
	opts := []otlptracehttp.Option{}
	if cfg.Endpoint != "" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("the tracing endpoint %q is not a URL", cfg.Endpoint)
		}
		// The endpoint is the collector's base address; the exporter adds the signal path to it.
		opts = append(opts, otlptracehttp.WithEndpoint(u.Host), otlptracehttp.WithURLPath(strings.TrimRight(u.Path, "/")+"/v1/traces"))
		if u.Scheme == "http" {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create the trace exporter: %w", err)
	}
	res := resource.NewSchemaless(
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(version),
		attribute.String("service.namespace", "shadowmock"),
	)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	return &Telemetry{tracer: tp.Tracer("shadowmock"), prop: propagation.TraceContext{}, shutdown: tp.Shutdown}, nil
}

// Start reads the trace context the request arrived with (a W3C traceparent header) and starts a
// server span under it. The span and a context that carries it are returned; the caller ends it.
func (t *Telemetry) Start(r *http.Request, name string) (context.Context, trace.Span) {
	if t == nil {
		return r.Context(), trace.SpanFromContext(r.Context())
	}
	ctx := t.prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	return t.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
}

// Inject writes the trace context in ctx into outgoing headers, so a call to a proxied upstream
// continues the trace.
func (t *Telemetry) Inject(ctx context.Context, h http.Header) {
	if t == nil {
		return
	}
	t.prop.Inject(ctx, propagation.HeaderCarrier(h))
}

// Shutdown flushes buffered spans.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil || t.shutdown == nil {
		return nil
	}
	if err := t.shutdown(ctx); err != nil {
		return fmt.Errorf("flush traces: %w", err)
	}
	return nil
}

// LogHandler adds trace_id and span_id to every record logged with a context that carries a span,
// so a log line can be found from its trace and the other way round.
type LogHandler struct{ next slog.Handler }

var _ slog.Handler = (*LogHandler)(nil)

// NewLogHandler wraps next.
func NewLogHandler(next slog.Handler) *LogHandler { return &LogHandler{next: next} }

// Enabled implements slog.Handler.
func (h *LogHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

// Handle implements slog.Handler.
func (h *LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	if err := h.next.Handle(ctx, r); err != nil {
		return fmt.Errorf("log: %w", err)
	}
	return nil
}

// WithAttrs implements slog.Handler.
func (h *LogHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return &LogHandler{next: h.next.WithAttrs(a)}
}

// WithGroup implements slog.Handler.
func (h *LogHandler) WithGroup(name string) slog.Handler {
	return &LogHandler{next: h.next.WithGroup(name)}
}

package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/pwgith/shadow-mock/internal/telemetry"
)

const (
	traceID    = "4bf92f3577b34da6a3ce929d0e0e4736"
	callerSpan = "00f067aa0ba902b7"
)

func request(t *testing.T, traceparent string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
	if traceparent != "" {
		r.Header.Set("Traceparent", traceparent)
	}
	return r
}

func TestASpanContinuesTheCallersTrace(t *testing.T) {
	t.Parallel()
	exp := tracetest.NewInMemoryExporter()
	tel, err := telemetry.New(t.Context(), telemetry.Config{}, "test", sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	if err != nil {
		t.Fatal(err)
	}
	_, span := tel.Start(request(t, "00-"+traceID+"-"+callerSpan+"-01"), "mock GET")
	span.End()
	spans := exp.GetSpans()
	if len(spans) != 1 || spans[0].SpanContext.TraceID().String() != traceID || spans[0].Parent.SpanID().String() != callerSpan {
		t.Errorf("spans = %+v, want one in trace %s under span %s", spans, traceID, callerSpan)
	}
}

func TestMalformedTraceContextStartsANewTrace(t *testing.T) {
	t.Parallel()
	exp := tracetest.NewInMemoryExporter()
	tel, _ := telemetry.New(t.Context(), telemetry.Config{}, "test", sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	for _, bad := range []string{"garbage", "00-short-short-01", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", "ff-" + traceID + "-" + callerSpan + "-01"} {
		_, span := tel.Start(request(t, bad), "mock GET")
		span.End()
	}
	for _, s := range exp.GetSpans() {
		if s.SpanContext.TraceID().String() == traceID || !s.SpanContext.TraceID().IsValid() {
			t.Errorf("a bad traceparent should give a fresh valid trace, got %s", s.SpanContext.TraceID())
		}
	}
}

func TestTraceContextIsInjectedIntoOutgoingHeaders(t *testing.T) {
	t.Parallel()
	exp := tracetest.NewInMemoryExporter()
	tel, _ := telemetry.New(t.Context(), telemetry.Config{}, "test", sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	ctx, span := tel.Start(request(t, "00-"+traceID+"-"+callerSpan+"-01"), "mock GET")
	defer span.End()
	h := http.Header{}
	tel.Inject(ctx, h)
	if tp := h.Get("Traceparent"); !strings.HasPrefix(tp, "00-"+traceID+"-") || strings.Contains(tp, callerSpan) {
		t.Errorf("traceparent = %q, want the caller's trace with this server's own span", tp)
	}
}

func TestADisabledOrMissingTelemetryRecordsNothingAndDoesNotFail(t *testing.T) {
	t.Parallel()
	for name, tel := range map[string]*telemetry.Telemetry{"disabled": telemetry.Disabled(), "nil": nil} {
		ctx, span := tel.Start(request(t, "00-"+traceID+"-"+callerSpan+"-01"), "mock GET")
		span.End()
		tel.Inject(ctx, http.Header{})
		if err := tel.Shutdown(t.Context()); err != nil {
			t.Errorf("%s: shutdown: %v", name, err)
		}
	}
	// A disabled server still reads the caller's trace, so its log lines carry the trace id.
	ctx, _ := telemetry.Disabled().Start(request(t, "00-"+traceID+"-"+callerSpan+"-01"), "x")
	var buf bytes.Buffer
	slog.New(telemetry.NewLogHandler(slog.NewJSONHandler(&buf, nil))).InfoContext(ctx, "hello")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil || line["trace_id"] != traceID {
		t.Errorf("log line %s (%v), want trace_id %s", buf.String(), err, traceID)
	}
}

func TestLogLinesCarryTheTraceOnlyWhenThereIsOne(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(telemetry.NewLogHandler(slog.NewJSONHandler(&buf, nil)))
	log.InfoContext(context.Background(), "no trace")
	if strings.Contains(buf.String(), "trace_id") {
		t.Errorf("a line logged outside a trace should have no trace_id: %s", buf.String())
	}
	exp := tracetest.NewInMemoryExporter()
	tel, _ := telemetry.New(t.Context(), telemetry.Config{}, "test", sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	ctx, span := tel.Start(request(t, ""), "mock GET")
	defer span.End()
	buf.Reset()
	log.With("k", "v").WithGroup("g").InfoContext(ctx, "in a trace")
	if !strings.Contains(buf.String(), "trace_id") || !strings.Contains(buf.String(), "span_id") {
		t.Errorf("a line logged in a trace needs trace_id and span_id: %s", buf.String())
	}
}

// The exporter sends spans to an OTLP/HTTP collector at the configured address.
func TestSpansAreExportedToTheCollector(t *testing.T) {
	t.Parallel()
	got := make(chan *http.Request, 1)
	body := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(r.Body)
		body <- b.Len()
		got <- r
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tel, err := telemetry.New(t.Context(), telemetry.Config{Enabled: true, Endpoint: srv.URL, ServiceName: "shadowmock-test", SampleRatio: 1}, "9.9.9", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, span := tel.Start(request(t, ""), "mock GET")
	span.End()
	if err := tel.Shutdown(t.Context()); err != nil { // flushes what is buffered
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case r := <-got:
		if r.Method != http.MethodPost || r.URL.Path != "/v1/traces" {
			t.Errorf("the collector received %s %s, want POST /v1/traces", r.Method, r.URL.Path)
		}
		if n := <-body; n == 0 {
			t.Error("the collector received an empty body")
		}
	default:
		t.Fatal("the collector received nothing after shutdown")
	}
}

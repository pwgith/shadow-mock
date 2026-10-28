package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A fixed trace, so a scenario can check that the server continues it.
const (
	callerTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	callerSpan  = "00f067aa0ba902b7"
)

// initTracing registers the steps that send a trace context and read back the spans and log lines.
func (w *world) initTracing(sc *godog.ScenarioContext) {
	sc.Step(`^Shadow Mock is running with tracing enabled$`, func() error {
		return w.reconfigure("log: { format: json, requests: true }\ntracing: { enabled: true }")
	})
	sc.Step(`^Shadow Mock is running with tracing enabled and a sample ratio of (\d+)$`, func(ratio int) error {
		return w.reconfigure(fmt.Sprintf("log: { format: json, requests: true }\ntracing: { enabled: true, sampleRatio: %d }", ratio))
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with a W3C traceparent header$`, func(method, path string) error {
		return w.sendTraced(method, path, "01")
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with a W3C traceparent header that is not sampled$`, func(method, path string) error {
		return w.sendTraced(method, path, "00")
	})
	sc.Step(`^an OpenTelemetry span is exported for the request$`, func() error {
		_, err := w.spanFor(callerTrace)
		return err
	})
	sc.Step(`^no span is exported for the request$`, func() error {
		if w.spans == nil {
			return fmt.Errorf("tracing is not on in this scenario")
		}
		if n := len(w.spans.GetSpans()); n != 0 {
			return fmt.Errorf("%d spans were exported, want none", n)
		}
		return nil
	})
	sc.Step(`^the span is named "([^"]*)" and continues the caller's trace$`, func(name string) error {
		s, err := w.spanFor(callerTrace)
		if err != nil {
			return err
		}
		if s.Name != name {
			return fmt.Errorf("the span is named %q, want %q", s.Name, name)
		}
		if got := s.Parent.SpanID().String(); got != callerSpan {
			return fmt.Errorf("the span's parent is %s, want the caller's span %s", got, callerSpan)
		}
		return nil
	})
	sc.Step(`^the span has the attribute "([^"]*)" equal to "([^"]*)"$`, func(key, want string) error {
		s, err := w.latestSpan()
		if err != nil {
			return err
		}
		for _, a := range s.Attributes {
			if string(a.Key) == key {
				if got := a.Value.String(); got != want {
					return fmt.Errorf("attribute %s = %q, want %q", key, got, want)
				}
				return nil
			}
		}
		return fmt.Errorf("the span has no attribute %s: %v", key, s.Attributes)
	})
	sc.Step(`^no span attribute mentions "([^"]*)"$`, func(text string) error {
		for _, s := range w.spans.GetSpans() {
			for _, a := range s.Attributes {
				if strings.Contains(a.Value.String(), text) || strings.Contains(string(a.Key), text) {
					return fmt.Errorf("span attribute %s = %s mentions %q", a.Key, a.Value.String(), text)
				}
			}
		}
		return nil
	})
	sc.Step(`^the log line for the request is JSON with fields "([^"]*)", "([^"]*)" and "([^"]*)"$`, func(a, b, c string) error {
		line, err := w.requestLogLine()
		if err != nil {
			return err
		}
		for _, f := range []string{a, b, c} {
			if _, ok := line[f]; !ok {
				return fmt.Errorf("the log line has no field %q: %v", f, line)
			}
		}
		return nil
	})
	sc.Step(`^the log line for the request has "([^"]*)" equal to "([^"]*)"$`, func(field, want string) error {
		line, err := w.requestLogLine()
		if err != nil {
			return err
		}
		if got := fmt.Sprint(line[field]); got != want {
			return fmt.Errorf("log field %s = %q, want %q", field, got, want)
		}
		return nil
	})
	sc.Step(`^the log line for the request has the caller's trace id$`, func() error {
		line, err := w.requestLogLine()
		if err != nil {
			return err
		}
		if line["trace_id"] != callerTrace {
			return fmt.Errorf("trace_id = %v, want the caller's %s", line["trace_id"], callerTrace)
		}
		return nil
	})
	sc.Step(`^the log line for the request has a trace id of 32 hexadecimal characters$`, func() error {
		line, err := w.requestLogLine()
		if err != nil {
			return err
		}
		id, ok := line["trace_id"].(string)
		if !ok || len(id) != 32 {
			return fmt.Errorf("trace_id = %q, want 32 hexadecimal characters", id)
		}
		return nil
	})
	sc.Step(`^no log line mentions "([^"]*)"$`, func(text string) error {
		if w.logContains(text) {
			return fmt.Errorf("the log mentions %q:\n%s", text, w.logs.String())
		}
		return nil
	})
	sc.Step(`^no request is logged$`, func() error {
		if w.logContains("request served") {
			return fmt.Errorf("a request was logged:\n%s", w.logs.String())
		}
		return nil
	})
	sc.Step(`^the upstream "([^"]*)" received a traceparent in the caller's trace$`, func(name string) error {
		u, ok := w.upstreams[name]
		if !ok {
			return fmt.Errorf("no upstream %q", name)
		}
		for _, r := range u.requests() {
			if tp := r.header.Get("Traceparent"); strings.HasPrefix(tp, "00-"+callerTrace+"-") && !strings.Contains(tp, callerSpan) {
				return nil
			}
		}
		return fmt.Errorf("upstream %s received no traceparent that continues trace %s", name, callerTrace)
	})
}

// sendTraced sends a request with a traceparent header carrying the fixed caller trace.
func (w *world) sendTraced(method, path, flags string) error {
	w.traceID, w.parentSpanID = callerTrace, callerSpan
	r := newRequest(method, path)
	r.header.Set("Traceparent", "00-"+callerTrace+"-"+callerSpan+"-"+flags)
	return w.sendOne(r)
}

// spanFor returns the exported span that belongs to trace id.
func (w *world) spanFor(id string) (tracetest.SpanStub, error) {
	if w.spans == nil {
		return tracetest.SpanStub{}, fmt.Errorf("tracing is not on in this scenario")
	}
	for _, s := range w.spans.GetSpans() {
		if s.SpanContext.TraceID().String() == id {
			return s, nil
		}
	}
	return tracetest.SpanStub{}, fmt.Errorf("no span was exported for trace %s; spans: %v", id, w.spans.GetSpans().Snapshots())
}

func (w *world) latestSpan() (tracetest.SpanStub, error) {
	if w.spans == nil {
		return tracetest.SpanStub{}, fmt.Errorf("tracing is not on in this scenario")
	}
	all := w.spans.GetSpans()
	if len(all) == 0 {
		return tracetest.SpanStub{}, fmt.Errorf("no span was exported")
	}
	return all[len(all)-1], nil
}

// requestLogLine returns the last "request served" line of the log, decoded.
func (w *world) requestLogLine() (map[string]any, error) {
	lines := strings.Split(w.logs.String(), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], "request served") {
			continue
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &out); err != nil {
			return nil, fmt.Errorf("the request log line is not JSON: %w: %s", err, lines[i])
		}
		return out, nil
	}
	return nil, fmt.Errorf("no request was logged; the log:\n%s", w.logs.String())
}

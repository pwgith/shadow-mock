package config_test

import (
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/config"
)

func TestTracingDefaultsAreOffAndKeepEverythingSampled(t *testing.T) {
	t.Parallel()
	d := config.Default()
	if d.Tracing.Enabled || d.Log.Requests || d.Tracing.SampleRatio != 1 || d.Tracing.ServiceName != "shadowmock" {
		t.Errorf("defaults = %+v, log requests %v", d.Tracing, d.Log.Requests)
	}
}

func TestTracingSettingsAreValidated(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, yaml, want string }{
		{"a ratio above one", "tracing: { sampleRatio: 1.5 }", "between 0 and 1"},
		{"a negative ratio", "tracing: { sampleRatio: -0.1 }", "between 0 and 1"},
		{"an endpoint that is not a URL", "tracing: { endpoint: localhost:4318 }", "http or https URL"},
		{"an empty service name", "tracing: { enabled: true, serviceName: \"\" }", "serviceName"},
	} {
		if _, err := load(t, tt.yaml, nil); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want an error mentioning %q", tt.name, err, tt.want)
		}
	}
	if _, err := load(t, "tracing: { enabled: true, endpoint: 'http://collector:4318', sampleRatio: 0.25 }\nlog: { requests: true }", nil); err != nil {
		t.Errorf("a valid tracing configuration: %v", err)
	}
}

func TestTracingCanBeSetFromTheEnvironment(t *testing.T) {
	t.Parallel()
	c, err := config.Load("", env("SHADOWMOCK_TRACING_ENABLED", "true", "SHADOWMOCK_TRACING_ENDPOINT", "http://collector:4318", "SHADOWMOCK_LOG_REQUESTS", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Tracing.Enabled || c.Tracing.Endpoint != "http://collector:4318" || !c.Log.Requests {
		t.Errorf("config = %+v log requests %v", c.Tracing, c.Log.Requests)
	}
	if _, err := config.Load("", env("SHADOWMOCK_TRACING_ENABLED", "maybe")); err == nil {
		t.Error("a value that is not a boolean should fail")
	}
}

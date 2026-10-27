package model

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

func TestDecodeKeepsUnknownFields(t *testing.T) {
	t.Parallel()
	in := `{"id":"m1","request":{"urlPath":"/a","customField":1},"response":{"status":201,"vendorThing":true},"extra":{"x":[1,2]}}`
	m, err := DecodeStubMapping([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if m.Priority != DefaultPriority || m.Response.StatusOrDefault() != 201 || *m.Request.URLPath != "/a" {
		t.Errorf("decoded %+v", m)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(in), &want); err != nil {
		t.Fatal(err)
	}
	want["uuid"] = "m1"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("round trip (-want +got):\n%s", diff)
	}
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		code apierr.Code // "" means a WireMock validation error
	}{
		{"not an object", `[1]`, ""},
		{"no request", `{"response":{}}`, ""},
		{"scenario name", `{"scenarioName":"x","request":{},"response":{}}`, apierr.ScenariosNotSupported},
		{"required state", `{"requiredScenarioState":"x","request":{},"response":{}}`, apierr.ScenariosNotSupported},
		{"new state", `{"newScenarioState":"x","request":{},"response":{}}`, apierr.ScenariosNotSupported},
		{"wrong type", `{"request":{"method":5},"response":{}}`, ""},
		{"two url fields", `{"request":{"url":"/a","urlPath":"/a"},"response":{}}`, ""},
		{"pathParameters without template", `{"request":{"urlPath":"/a","pathParameters":{"id":{"equalTo":"1"}}},"response":{}}`, ""},
		{"bad scheme", `{"request":{"scheme":"ftp"},"response":{}}`, ""},
		{"status out of range", `{"request":{},"response":{"status":700}}`, ""},
		{"unknown fault", `{"request":{},"response":{"fault":"MELTDOWN"}}`, ""},
		{"negative delay", `{"request":{},"response":{"fixedDelayMilliseconds":-1}}`, ""},
		{"unknown distribution", `{"request":{},"response":{"delayDistribution":{"type":"poisson"}}}`, ""},
		{"priority zero", `{"priority":0,"request":{},"response":{}}`, ""},
		{"backwards range", `{"request":{},"response":{},"callResponses":[{"calls":"4-2","response":{}}]}`, ""},
		{"both selectors", `{"request":{},"response":{},"callResponses":[{"calls":"1","every":2,"response":{}}]}`, ""},
		{"no selector", `{"request":{},"response":{},"callResponses":[{"response":{}}]}`, ""},
		{"every 1", `{"request":{},"response":{},"callResponses":[{"every":1,"response":{}}]}`, ""},
		{"rule response invalid", `{"request":{},"response":{},"callResponses":[{"calls":"1","response":{"status":1}}]}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeStubMapping([]byte(tt.in))
			var ae *apierr.Error
			var ve *apierr.ValidationError
			switch {
			case tt.code != "" && (!errors.As(err, &ae) || ae.Code != tt.code):
				t.Errorf("error = %v, want code %s", err, tt.code)
			case tt.code == "" && !errors.As(err, &ve):
				t.Errorf("error = %v, want a validation error", err)
			}
		})
	}
}

func TestParseCalls(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in       string
		from, to int64
		ok       bool
	}{
		{"3", 3, 3, true},
		{"1-2", 1, 2, true},
		{"100+", 100, 0, true},
		{"0", 0, 0, false},
		{"03", 0, 0, false},
		{"-1", 0, 0, false},
		{"a", 0, 0, false},
		{"1-", 0, 0, false},
		{"+", 0, 0, false},
		{"2-1", 0, 0, false},
	}
	for _, tt := range tests {
		from, to, err := parseCalls(tt.in)
		if (err == nil) != tt.ok || from != tt.from || to != tt.to {
			t.Errorf("parseCalls(%q) = %d, %d, %v", tt.in, from, to, err)
		}
	}
}

func TestResponseFor(t *testing.T) {
	t.Parallel()
	m, err := DecodeStubMapping([]byte(`{"request":{},"response":{"status":200},"callResponses":[
		{"calls":"1-2","response":{"status":503}},{"every":3,"response":{"status":500}},{"calls":"10+","response":{"status":429}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasCallResponses() {
		t.Fatal("HasCallResponses = false")
	}
	want := map[int64]int{1: 503, 2: 503, 3: 500, 4: 200, 6: 500, 10: 429, 12: 500, 11: 429}
	for n, status := range want {
		if got := m.ResponseFor(n).StatusOrDefault(); got != status {
			t.Errorf("ResponseFor(%d) = %d, want %d", n, got, status)
		}
	}
}

func TestDecodeMappings(t *testing.T) {
	t.Parallel()
	ms, err := DecodeMappings([]byte(`{"mappings":[{"request":{},"response":{}},{"id":"b","request":{},"response":{}}]}`))
	if err != nil || len(ms) != 2 || ms[1].ID != "b" {
		t.Errorf("DecodeMappings = %v, %v", ms, err)
	}
	if ms, err := DecodeMappings([]byte(`{"request":{},"response":{}}`)); err != nil || len(ms) != 1 {
		t.Errorf("single mapping = %v, %v", ms, err)
	}
	for _, bad := range []string{`nope`, `{"mappings":[{"response":{}}]}`, `{"response":{}}`} {
		if _, err := DecodeMappings([]byte(bad)); err == nil {
			t.Errorf("DecodeMappings(%s) succeeded", bad)
		}
	}
}

func TestHeaderValueAndMarshal(t *testing.T) {
	t.Parallel()
	var r ResponseDefinition
	if err := json.Unmarshal([]byte(`{"headers":{"A":"1","B":["2","3"]}}`), &r); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]HeaderValue{"A": {"1"}, "B": {"2", "3"}}, r.Headers); diff != "" {
		t.Errorf("headers (-want +got):\n%s", diff)
	}
	if err := json.Unmarshal([]byte(`{"headers":{"A":5}}`), &r); err == nil {
		t.Error("a numeric header value should fail")
	}
	b, err := json.Marshal(ResponseDefinition{Status: 204, Headers: map[string]HeaderValue{"A": {"1"}, "B": {"2", "3"}}})
	if err != nil || string(b) != `{"status":204,"headers":{"A":"1","B":["2","3"]}}` {
		t.Errorf("Marshal = %s, %v", b, err)
	}
	b, err = json.Marshal(RequestPattern{Method: "GET"})
	if err != nil || string(b) != `{"method":"GET"}` {
		t.Errorf("Marshal pattern = %s, %v", b, err)
	}
	if err := json.Unmarshal([]byte(`{"method":5}`), &RequestPattern{}); err == nil {
		t.Error("bad pattern should fail")
	}
	if b, err := json.Marshal(StubMapping{}); err != nil || string(b) != "{}" {
		t.Errorf("empty mapping = %s", b)
	}
	var m StubMapping
	if err := json.Unmarshal([]byte(`{"request":{},"response":{}}`), &m); err != nil {
		t.Errorf("UnmarshalJSON: %v", err)
	}
	if err := json.Unmarshal([]byte(`{}`), &m); err == nil {
		t.Error("UnmarshalJSON without request should fail")
	}
	if !(&RequestPattern{Method: "any"}).AnyMethod() || (&RequestPattern{Method: "GET"}).AnyMethod() {
		t.Error("AnyMethod")
	}
}

func TestMaskAndSample(t *testing.T) {
	t.Parallel()
	var a, b RequestPattern
	if err := json.Unmarshal([]byte(`{"urlPath":"/a"}`), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"urlPath":"/a"}`), &b); err != nil {
		t.Fatal(err)
	}
	if !(Mask{Request: &a}).Equal(Mask{Request: &b}) || (Mask{ID: "x"}).Equal(Mask{ID: "y"}) || !(Mask{ID: "x"}).Equal(Mask{ID: "x"}) {
		t.Error("Mask.Equal")
	}
	if (&SampleRequest{Method: "GET"}).Validate() == nil || (&SampleRequest{Method: "GET", URL: "/"}).Validate() != nil {
		t.Error("SampleRequest.Validate")
	}
}

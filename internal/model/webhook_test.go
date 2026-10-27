package model_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/model"
)

func TestWebhooksAreDecoded(t *testing.T) {
	t.Parallel()
	m, err := model.DecodeStubMapping([]byte(`{
		"request": {"urlPath": "/p"},
		"serveEventListeners": [
			{"name": "webhook", "parameters": {"method": "POST", "url": "http://a/x", "headers": {"A": "1", "B": ["2", "3"]}, "body": "b", "delay": {"type": "fixed", "milliseconds": 5}}},
			{"name": "something-else", "parameters": {}}
		],
		"postServeActions": [ {"name": "webhook", "parameters": {"method": "GET", "url": "http://a/y"}} ]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Webhooks) != 2 {
		t.Fatalf("got %d webhooks, want 2: %+v", len(m.Webhooks), m.Webhooks)
	}
	first := m.Webhooks[0]
	if first.Method != http.MethodPost || first.URL != "http://a/x" || first.Body != "b" || first.Delay == nil || first.Delay.Milliseconds != 5 {
		t.Errorf("first webhook = %+v", first)
	}
	if diff := cmp.Diff(model.HeaderValue{"2", "3"}, first.Headers["B"]); diff != "" {
		t.Errorf("headers (-want +got):\n%s", diff)
	}
	if m.Webhooks[1].URL != "http://a/y" {
		t.Errorf("second webhook = %+v", m.Webhooks[1])
	}
	// The mapping still round-trips as received.
	if out, _ := m.MarshalJSON(); !strings.Contains(string(out), "serveEventListeners") {
		t.Errorf("the serialised mapping lost serveEventListeners: %s", out)
	}
}

func TestInvalidWebhooksAreRejected(t *testing.T) {
	t.Parallel()
	for name, js := range map[string]string{
		"no url":         `{"request":{"urlPath":"/p"},"serveEventListeners":[{"name":"webhook","parameters":{"method":"GET"}}]}`,
		"no method":      `{"request":{"urlPath":"/p"},"serveEventListeners":[{"name":"webhook","parameters":{"url":"http://a"}}]}`,
		"bad parameters": `{"request":{"urlPath":"/p"},"postServeActions":[{"name":"webhook","parameters":"nope"}]}`,
		"not a list":     `{"request":{"urlPath":"/p"},"serveEventListeners":{"name":"webhook"}}`,
	} {
		if _, err := model.DecodeStubMapping([]byte(js)); err == nil {
			t.Errorf("%s: the mapping was accepted", name)
		}
	}
}

func TestUsesCallNumber(t *testing.T) {
	t.Parallel()
	for js, want := range map[string]bool{
		`{"request":{"urlPath":"/p"},"response":{"body":"{{request.callNumber}}"}}`:                                                         true,
		`{"request":{"urlPath":"/p"},"response":{"body":"x"},"callResponses":[{"calls":"1","response":{"body":"{{request.callNumber}}"}}]}`: true,
		`{"request":{"urlPath":"/p"},"response":{"body":"plain"}}`:                                                                          false,
	} {
		m, err := model.DecodeStubMapping([]byte(js))
		if err != nil {
			t.Fatal(err)
		}
		if m.UsesCallNumber != want {
			t.Errorf("%s: UsesCallNumber = %v, want %v", js, m.UsesCallNumber, want)
		}
	}
}

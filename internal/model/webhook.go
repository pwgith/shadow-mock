package model

import (
	"encoding/json"
	"strconv"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

// WebhookName is the name of the one post-serve action Shadow Mock supports.
const WebhookName = "webhook"

// Webhook is an outbound HTTP call a mapping makes after its response is served (FR-6.2).
// WireMock spells the list serveEventListeners, and postServeActions in older versions.
type Webhook struct {
	Method     string                 `json:"method"`
	URL        string                 `json:"url"`
	Headers    map[string]HeaderValue `json:"headers,omitempty"`
	Body       string                 `json:"body,omitempty"`
	Base64Body string                 `json:"base64Body,omitempty"`
	Delay      *DelayDistribution     `json:"delay,omitempty"`
}

// action is one entry of serveEventListeners or postServeActions.
type action struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
}

// decodeWebhooks reads the webhooks from the mapping's two action lists. Actions with other
// names are ignored, as WireMock ignores listeners it has no extension for.
func decodeWebhooks(raw map[string]json.RawMessage) ([]Webhook, error) {
	var out []Webhook
	for _, field := range []string{"serveEventListeners", "postServeActions"} {
		data, ok := raw[field]
		if !ok {
			continue
		}
		var actions []action
		if err := json.Unmarshal(data, &actions); err != nil {
			return nil, apierr.Invalid("/"+field, "%s must be a list of actions: %v", field, err)
		}
		for i, a := range actions {
			if a.Name != WebhookName {
				continue
			}
			ptr := pointerJoin("/"+field, strconv.Itoa(i), "parameters")
			var h Webhook
			if err := json.Unmarshal(a.Parameters, &h); err != nil {
				return nil, apierr.Invalid(ptr, "invalid webhook parameters: %v", err)
			}
			if h.URL == "" {
				return nil, apierr.Invalid(ptr+"/url", "a webhook needs a url")
			}
			if h.Method == "" {
				return nil, apierr.Invalid(ptr+"/method", "a webhook needs a method")
			}
			out = append(out, h)
		}
	}
	return out, nil
}

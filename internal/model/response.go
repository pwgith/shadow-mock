package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

// Faults a response can simulate.
const (
	FaultConnectionReset     = "CONNECTION_RESET_BY_PEER"
	FaultEmptyResponse       = "EMPTY_RESPONSE"
	FaultMalformedChunk      = "MALFORMED_RESPONSE_CHUNK"
	FaultRandomDataThenClose = "RANDOM_DATA_THEN_CLOSE"
)

// HeaderValue is a response header value: a string or a list of strings.
type HeaderValue []string

// UnmarshalJSON accepts a string or an array of strings.
func (h *HeaderValue) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*h = HeaderValue{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("header value must be a string or an array of strings: %w", err)
	}
	*h = many
	return nil
}

// MarshalJSON writes a single value as a string, as WireMock does.
func (h HeaderValue) MarshalJSON() ([]byte, error) {
	var (
		b   []byte
		err error
	)
	if len(h) == 1 {
		b, err = json.Marshal(h[0])
	} else {
		b, err = json.Marshal([]string(h))
	}
	if err != nil {
		return nil, fmt.Errorf("marshal header value: %w", err)
	}
	return b, nil
}

// DelayDistribution is a random delay: uniform, lognormal or fixed.
type DelayDistribution struct {
	Type         string  `json:"type"`
	Lower        int64   `json:"lower,omitempty"`
	Upper        int64   `json:"upper,omitempty"`
	Median       float64 `json:"median,omitempty"`
	Sigma        float64 `json:"sigma,omitempty"`
	MaxValue     int64   `json:"maxValue,omitempty"`
	Milliseconds int64   `json:"milliseconds,omitempty"`
}

// ChunkedDribbleDelay sends the body in chunks spread over a duration.
type ChunkedDribbleDelay struct {
	NumberOfChunks int   `json:"numberOfChunks"`
	TotalDuration  int64 `json:"totalDuration"`
}

// ResponseDefinition is a WireMock response definition.
type ResponseDefinition struct {
	Status                        int                    `json:"status,omitempty"`
	StatusMessage                 string                 `json:"statusMessage,omitempty"`
	Headers                       map[string]HeaderValue `json:"headers,omitempty"`
	Body                          *string                `json:"body,omitempty"`
	JSONBody                      json.RawMessage        `json:"jsonBody,omitempty"`
	Base64Body                    string                 `json:"base64Body,omitempty"`
	BodyFileName                  string                 `json:"bodyFileName,omitempty"`
	FixedDelayMilliseconds        int64                  `json:"fixedDelayMilliseconds,omitempty"`
	DelayDistribution             *DelayDistribution     `json:"delayDistribution,omitempty"`
	ChunkedDribbleDelay           *ChunkedDribbleDelay   `json:"chunkedDribbleDelay,omitempty"`
	Fault                         string                 `json:"fault,omitempty"`
	ProxyBaseURL                  string                 `json:"proxyBaseUrl,omitempty"`
	AdditionalProxyRequestHeaders map[string]string      `json:"additionalProxyRequestHeaders,omitempty"`
	RemoveProxyRequestHeaders     []string               `json:"removeProxyRequestHeaders,omitempty"`
	ProxyURLPrefixToRemove        string                 `json:"proxyUrlPrefixToRemove,omitempty"`
	Transformers                  []string               `json:"transformers,omitempty"`
	TransformerParameters         map[string]any         `json:"transformerParameters,omitempty"`
	GzipDisabled                  bool                   `json:"gzipDisabled,omitempty"`

	raw json.RawMessage
}

// responseFields avoids recursion in UnmarshalJSON.
type responseFields ResponseDefinition

// UnmarshalJSON decodes the definition and keeps the original JSON.
func (r *ResponseDefinition) UnmarshalJSON(data []byte) error {
	var f responseFields
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("response definition: %w", err)
	}
	*r = ResponseDefinition(f)
	r.raw = slices.Clone(data)
	return nil
}

// MarshalJSON returns the definition as received, including unknown fields.
func (r ResponseDefinition) MarshalJSON() ([]byte, error) {
	if r.raw != nil {
		return r.raw, nil
	}
	b, err := json.Marshal(responseFields(r))
	if err != nil {
		return nil, fmt.Errorf("marshal response definition: %w", err)
	}
	return b, nil
}

// StatusOrDefault returns the status, or 200 when it is not set.
func (r *ResponseDefinition) StatusOrDefault() int {
	if r.Status == 0 {
		return http.StatusOK
	}
	return r.Status
}

// Validate checks rules the JSON shape alone cannot express.
func (r *ResponseDefinition) Validate(ptr string) error {
	if r.Status != 0 && (r.Status < 100 || r.Status > 599) {
		return apierr.Invalid(ptr+"/status", "status must be between 100 and 599")
	}
	switch r.Fault {
	case "", FaultConnectionReset, FaultEmptyResponse, FaultMalformedChunk, FaultRandomDataThenClose:
	default:
		return apierr.Invalid(ptr+"/fault", "unknown fault %q", r.Fault)
	}
	if r.FixedDelayMilliseconds < 0 {
		return apierr.Invalid(ptr+"/fixedDelayMilliseconds", "the delay must not be negative")
	}
	if d := r.DelayDistribution; d != nil {
		switch d.Type {
		case "uniform", "lognormal", "fixed":
		default:
			return apierr.Invalid(ptr+"/delayDistribution/type", "unknown delay distribution %q", d.Type)
		}
	}
	return nil
}

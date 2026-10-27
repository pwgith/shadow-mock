// Package model is the WireMock-compatible wire model: stub mappings, request patterns and
// response definitions, plus the Shadow Mock extension callResponses (DESIGN.md §4.2).
//
// Every type keeps the JSON it was decoded from, so unknown fields round-trip unchanged
// (FR-1.4). The typed fields are a read-only view used to compile matchers and responses.
package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

// DefaultPriority is WireMock's default stub priority. Lower numbers win.
const DefaultPriority = 5

// StubMapping is a WireMock stub mapping.
type StubMapping struct {
	ID            string
	Name          string
	Priority      int
	Persistent    bool
	Request       RequestPattern
	Response      ResponseDefinition
	CallResponses []CallRule
	Metadata      map[string]any
	// Webhooks are the calls made after the response is served (FR-6.2).
	Webhooks []Webhook
	// UsesCallNumber is set when a template reads request.callNumber, so the call counter
	// runs even for a mapping without callResponses (FR-4.3).
	UsesCallNumber bool

	// raw is the mapping as received, with the id fields kept in step with ID.
	raw map[string]json.RawMessage
}

// stubFields is the typed view of a mapping's known fields.
type stubFields struct {
	ID            string              `json:"id"`
	UUID          string              `json:"uuid"`
	Name          string              `json:"name"`
	Priority      *int                `json:"priority"`
	Persistent    bool                `json:"persistent"`
	Request       *RequestPattern     `json:"request"`
	Response      *ResponseDefinition `json:"response"`
	CallResponses []CallResponse      `json:"callResponses"`
	Metadata      map[string]any      `json:"metadata"`
}

// DecodeStubMapping parses and validates one mapping. Scenario fields are rejected with
// 422 scenarios_not_supported; other problems return a WireMock-format validation error.
func DecodeStubMapping(data []byte) (StubMapping, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return StubMapping{}, apierr.Invalid("", "the mapping is not a valid JSON object: %v", err)
	}
	if raw == nil {
		return StubMapping{}, apierr.Invalid("", "the mapping must be a JSON object")
	}
	// WireMock's scenario fields are rejected rather than ignored (FR-4.4).
	for _, f := range []string{"scenarioName", "requiredScenarioState", "newScenarioState"} {
		if _, ok := raw[f]; ok {
			return StubMapping{}, apierr.New(http.StatusUnprocessableEntity, apierr.ScenariosNotSupported,
				"WireMock scenarios are not supported (%s). Use callResponses to vary the response by call number.", f)
		}
	}
	var f stubFields
	if err := json.Unmarshal(data, &f); err != nil {
		return StubMapping{}, apierr.Invalid("", "invalid mapping: %v", err)
	}
	if f.Request == nil {
		return StubMapping{}, apierr.Invalid("/request", "request is required")
	}
	if f.Response == nil {
		f.Response = &ResponseDefinition{}
	}
	if err := f.Request.Validate("/request"); err != nil {
		return StubMapping{}, err
	}
	if err := f.Response.Validate("/response"); err != nil {
		return StubMapping{}, err
	}
	rules, err := compileCallRules(f.CallResponses)
	if err != nil {
		return StubMapping{}, err
	}
	hooks, err := decodeWebhooks(raw)
	if err != nil {
		return StubMapping{}, err
	}
	m := StubMapping{
		ID:             f.ID,
		Name:           f.Name,
		Priority:       DefaultPriority,
		Persistent:     f.Persistent,
		Request:        *f.Request,
		Response:       *f.Response,
		CallResponses:  rules,
		Metadata:       f.Metadata,
		Webhooks:       hooks,
		UsesCallNumber: bytes.Contains(raw["response"], []byte("callNumber")) || bytes.Contains(raw["callResponses"], []byte("callNumber")),
		raw:            raw,
	}
	if m.ID == "" {
		m.ID = f.UUID
	}
	if f.Priority != nil {
		if *f.Priority < 1 {
			return StubMapping{}, apierr.Invalid("/priority", "priority must be 1 or more")
		}
		m.Priority = *f.Priority
	}
	return m.WithID(m.ID), nil
}

// WithID returns a copy of the mapping with its id (and uuid) set to id.
func (m StubMapping) WithID(id string) StubMapping {
	m.ID = id
	raw := maps.Clone(m.raw)
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	if id != "" {
		q, _ := json.Marshal(id) //nolint:errcheck // marshalling a string cannot fail
		raw["id"], raw["uuid"] = q, q
	}
	m.raw = raw
	return m
}

// MarshalJSON returns the mapping as received, including unknown fields.
func (m StubMapping) MarshalJSON() ([]byte, error) {
	if m.raw == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m.raw)
	if err != nil {
		return nil, fmt.Errorf("marshal mapping %s: %w", m.ID, err)
	}
	return b, nil
}

// UnmarshalJSON decodes and validates a mapping.
func (m *StubMapping) UnmarshalJSON(data []byte) error {
	d, err := DecodeStubMapping(data)
	if err != nil {
		return err
	}
	*m = d
	return nil
}

// HasCallResponses reports whether the mapping varies its response by call number.
func (m *StubMapping) HasCallResponses() bool { return len(m.CallResponses) > 0 }

// ResponseFor returns the response for call n: the first matching rule, else the default.
func (m *StubMapping) ResponseFor(n int64) *ResponseDefinition {
	for i := range m.CallResponses {
		if m.CallResponses[i].Matches(n) {
			return &m.CallResponses[i].Response
		}
	}
	return &m.Response
}

// DecodeMappings parses a mapping file: one mapping, or {"mappings": [...]}.
func DecodeMappings(data []byte) ([]StubMapping, error) {
	data = bytes.TrimSpace(data)
	var probe struct {
		Mappings []json.RawMessage `json:"mappings"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse mapping file: %w", err)
	}
	if probe.Mappings == nil {
		m, err := DecodeStubMapping(data)
		if err != nil {
			return nil, err
		}
		return []StubMapping{m}, nil
	}
	out := make([]StubMapping, 0, len(probe.Mappings))
	for i, r := range probe.Mappings {
		m, err := DecodeStubMapping(r)
		if err != nil {
			return nil, fmt.Errorf("mapping %d: %w", i, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// pointerJoin builds a JSON pointer.
func pointerJoin(base string, parts ...string) string {
	var b strings.Builder
	b.WriteString(base)
	for _, p := range parts {
		b.WriteString("/")
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(p))
	}
	return b.String()
}

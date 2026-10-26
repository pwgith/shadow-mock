package model

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Mask hides a parent-layer mapping from a session, by id or by request pattern (FR-7.8).
type Mask struct {
	ID      string          `json:"id,omitempty"`
	Request *RequestPattern `json:"request,omitempty"`
}

// Equal reports whether two masks are the same, for removal.
func (m Mask) Equal(o Mask) bool {
	if m.ID != "" || o.ID != "" {
		return m.ID == o.ID
	}
	a, errA := json.Marshal(m.Request)
	b, errB := json.Marshal(o.Request)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

// SampleRequest is a request evaluated without being sent as mock traffic.
type SampleRequest struct {
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers,omitempty"`
	Cookies      map[string]string `json:"cookies,omitempty"`
	Body         string            `json:"body,omitempty"`
	BodyAsBase64 string            `json:"bodyAsBase64,omitempty"`
}

// Validate checks the required fields.
func (s *SampleRequest) Validate() error {
	if s.Method == "" || s.URL == "" {
		return errors.New("method and url are required")
	}
	return nil
}

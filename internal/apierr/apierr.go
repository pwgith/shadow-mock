// Package apierr defines the engine's error responses: the {"error","message"} body with the
// codes in the OpenAPI ErrorCode enum, and WireMock's {"errors":[…]} validation body.
package apierr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Code is an error code from the ErrorCode enum in api/openapi.yaml.
type Code string

// Error codes. They must match the ErrorCode enum and the feature files.
const (
	InvalidToken                 Code = "invalid_token"
	OrgMismatch                  Code = "org_mismatch"
	Forbidden                    Code = "forbidden"
	IPNotAllowed                 Code = "ip_not_allowed"
	GlobalWritesViaManagementApp Code = "global_writes_via_management_app"
	NotFound                     Code = "not_found"
	NotSupported                 Code = "not_supported"
	UnknownOrg                   Code = "unknown_org"
	UnknownURLBasedMock          Code = "unknown_url_based_mock"
	NotAvailableInSaaS           Code = "not_available_in_saas"
	ValidationFailed             Code = "validation_failed"
	TTLMustBePositive            Code = "ttl_must_be_positive"
	TTLExceedsMaximum            Code = "ttl_exceeds_maximum"
	TokenTooShort                Code = "token_too_short"
	TokenInUse                   Code = "token_in_use"
	RecordingInProgress          Code = "recording_in_progress"
	ScenariosNotSupported        Code = "scenarios_not_supported"
	ParentNotFound               Code = "parent_not_found"
	BodyTooLarge                 Code = "body_too_large"
	AmbiguousSession             Code = "ambiguous_session"
)

// Error is an API error with an HTTP status.
type Error struct {
	Status  int
	Code    Code
	Message string
	Details map[string]any
}

// New returns an API error.
func New(status int, code Code, format string, a ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, a...)}
}

// Error implements error.
func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

type body struct {
	Error   Code           `json:"error"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Write renders the error as JSON.
func (e *Error) Write(w http.ResponseWriter) {
	WriteJSON(w, e.Status, body{Error: e.Code, Message: e.Message, Details: e.Details})
}

// WireMockError is one entry of WireMock's validation error body.
type WireMockError struct {
	Code   int             `json:"code"`
	Title  string          `json:"title"`
	Detail string          `json:"detail,omitempty"`
	Source *WireMockSource `json:"source,omitempty"`
}

// WireMockSource points at the invalid part of the input.
type WireMockSource struct {
	Pointer string `json:"pointer"`
}

// ValidationError is a WireMock-format validation failure (422).
type ValidationError struct {
	Errors []WireMockError `json:"errors"`
}

// Invalid returns a single-entry validation error for the JSON pointer.
func Invalid(pointer, format string, a ...any) *ValidationError {
	return &ValidationError{Errors: []WireMockError{{
		Code:   10,
		Title:  fmt.Sprintf(format, a...),
		Source: &WireMockSource{Pointer: pointer},
	}}}
}

// Error implements error.
func (v *ValidationError) Error() string {
	if len(v.Errors) == 0 {
		return "invalid"
	}
	if v.Errors[0].Source != nil {
		return v.Errors[0].Source.Pointer + ": " + v.Errors[0].Title
	}
	return v.Errors[0].Title
}

// Write renders the validation error.
func (v *ValidationError) Write(w http.ResponseWriter) {
	WriteJSON(w, http.StatusUnprocessableEntity, v)
}

// WriteError renders any error: API errors as they are, validation errors in WireMock
// format, and anything else as a 500.
func WriteError(w http.ResponseWriter, err error) {
	var ae *Error
	if errors.As(err, &ae) {
		ae.Write(w)
		return
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		ve.Write(w)
		return
	}
	WriteJSON(w, http.StatusInternalServerError, body{Error: "internal_error", Message: "internal error"})
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	// The status is already sent, so an encoding failure can only be a broken connection.
	_ = enc.Encode(v) //nolint:errcheck // nothing useful to do once headers are written
}

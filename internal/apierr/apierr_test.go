package apierr_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body, err)
	}
	return v
}

func TestErrorWrite(t *testing.T) {
	t.Parallel()
	e := apierr.New(http.StatusConflict, apierr.TokenInUse, "Match token %q is used by %s.", "CUST-1", "ses_1")
	if got, want := e.Error(), `token_in_use: Match token "CUST-1" is used by ses_1.`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	rec := httptest.NewRecorder()
	e.Write(rec)
	if rec.Code != 409 || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	want := map[string]any{"error": "token_in_use", "message": `Match token "CUST-1" is used by ses_1.`}
	if diff := cmp.Diff(want, decode(t, rec)); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}

	e.Details = map[string]any{"session": "ses_1"}
	rec = httptest.NewRecorder()
	e.Write(rec)
	if got := decode(t, rec)["details"]; !cmp.Equal(got, map[string]any{"session": "ses_1"}) {
		t.Errorf("details = %v", got)
	}
}

func TestValidationError(t *testing.T) {
	t.Parallel()
	v := apierr.Invalid("/request", "only %s", "one of url")
	if got, want := v.Error(), "/request: only one of url"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := (&apierr.ValidationError{}).Error(); got != "invalid" {
		t.Errorf("empty Error() = %q", got)
	}
	noPointer := &apierr.ValidationError{Errors: []apierr.WireMockError{{Detail: "bad input"}}}
	if got := noPointer.Error(); got != "bad input" {
		t.Errorf("Error() without a pointer = %q", got)
	}

	rec := httptest.NewRecorder()
	v.Write(rec)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	errs, _ := decode(t, rec)["errors"].([]any)
	first, _ := errs[0].(map[string]any)
	src, _ := first["source"].(map[string]any)
	if first["detail"] != "only one of url" || first["title"] != "Error parsing JSON" || src["pointer"] != "/request" {
		t.Errorf("errors[0] = %v", first)
	}
}

func TestWriteError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		check  func(map[string]any) bool
	}{
		{"an API error", apierr.New(404, apierr.NotFound, "gone"), 404, func(v map[string]any) bool { return v["error"] == "not_found" }},
		{"a wrapped API error", fmt.Errorf("outer: %w", apierr.New(403, apierr.Forbidden, "no")), 403, func(v map[string]any) bool { return v["error"] == "forbidden" }},
		{"a validation error", apierr.Invalid("/a", "x"), 422, func(v map[string]any) bool { return v["errors"] != nil }},
		{
			"anything else is a 500 that hides the cause", errors.New("database password is hunter2"), 500,
			func(v map[string]any) bool { return v["error"] == "internal_error" && v["message"] == "internal error" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			apierr.WriteError(rec, tt.err)
			if rec.Code != tt.status || !tt.check(decode(t, rec)) {
				t.Errorf("status %d, body %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	apierr.WriteJSON(rec, http.StatusCreated, map[string]string{"html": "<a&b>"})
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := rec.Body.String(); got != "{\"html\":\"<a&b>\"}\n" { // HTML is not escaped
		t.Errorf("body = %q", got)
	}
}

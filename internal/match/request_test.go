package match

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Go takes Host out of the header map; WireMock keeps it there, so header matchers, templates and
// the journal all see it.
func TestRequestKeepsTheHostHeader(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://shadow.example:8080/x", http.NoBody)
	r.Host = "shadow.example:8080"
	req := NewRequest(r, nil, "/x")
	if got := req.Header.Get("Host"); got != "shadow.example:8080" {
		t.Errorf("Host header = %q, want shadow.example:8080", got)
	}
	if r.Header.Get("Host") != "" {
		t.Error("the original request must not be changed")
	}
}

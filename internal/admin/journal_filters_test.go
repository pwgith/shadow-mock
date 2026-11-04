package admin_test

import (
	"net/url"
	"strings"
	"testing"
)

// journalSetup serves two matched and two unmatched requests, so that each filter has something to keep and drop.
func journalSetup(t *testing.T) *api {
	t.Helper()
	a := newAPI(t)
	a.want(201, "POST", "/mappings", `{"request":{"method":"GET","urlPath":"/orders/1"},"response":{"status":200}}`)
	a.want(201, "POST", "/mappings", `{"request":{"method":"POST","urlPath":"/orders/2"},"response":{"status":201}}`)
	a.mock("GET", "/orders/1")
	a.mock("POST", "/orders/2")
	a.mock("GET", "/orders/missing")
	a.mock("DELETE", "/other")
	return a
}

func TestJournalFilters(t *testing.T) {
	t.Parallel()
	a := journalSetup(t)
	tests := []struct {
		query string
		want  int
	}{
		{"", 4},
		{"status=200", 1},
		{"status=404", 2},
		{"method=get", 2},
		{"method=POST", 1},
		{"path=/orders", 3},
		{"path=/orders/1", 1},
		{"matched=true", 2},
		{"matched=false", 2},
		{"layer=global", 4},
		{"layer=url-based", 0},
		{"session=nobody", 0},
		{"from=2000-01-01T00:00:00Z", 4},
		{"from=2999-01-01T00:00:00Z", 0},
		{"to=2999-01-01T00:00:00Z", 4},
		{"to=2000-01-01T00:00:00Z", 0},
		{"method=GET&path=/orders&matched=true", 1},
		{"unmatched=true&method=DELETE", 1},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()
			body := a.want(200, "GET", "/requests?"+tc.query, "")
			reqs, _ := body["requests"].([]any)
			if len(reqs) != tc.want {
				t.Errorf("GET /requests?%s listed %d entries, want %d", tc.query, len(reqs), tc.want)
			}
		})
	}
}

func TestJournalFiltersRejectWhatTheyCannotRead(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	for name, query := range map[string]string{
		"status":  "status=ok",
		"matched": "matched=maybe",
		"from":    "from=yesterday",
		"to":      "to=" + url.QueryEscape("2026-13-45"),
	} {
		code, _, raw := a.do("GET", "/requests?"+query, "")
		if code != 400 || !strings.Contains(raw, name+" must be") {
			t.Errorf("GET /requests?%s = %d %s, want a 400 that names %s", query, code, raw, name)
		}
	}
}

package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func send(t *testing.T, h http.Handler, method, target string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	r.Host = "mocks.example.com"
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// The admin API has no login in standalone mode, so a web page must not be able to drive it
// through a visitor's browser.
func TestAdminRefusesChangesFromOtherSites(t *testing.T) {
	t.Parallel()
	rt, _ := newRouter(t)
	for _, tt := range []struct {
		name   string
		method string
		hdr    []string
		want   int
	}{
		{"a command-line client sends neither header", http.MethodPost, nil, 200},
		{"a page on the same origin", http.MethodPost, []string{"Origin", "http://mocks.example.com", "Sec-Fetch-Site", "same-origin"}, 200},
		{"the browser address bar", http.MethodPost, []string{"Sec-Fetch-Site", "none"}, 200},
		{"a page on another site", http.MethodPost, []string{"Origin", "http://evil.example", "Sec-Fetch-Site", "cross-site"}, 403},
		{"a sibling site", http.MethodPost, []string{"Sec-Fetch-Site", "same-site"}, 403},
		{"Origin alone is enough", http.MethodPost, []string{"Origin", "http://evil.example"}, 403},
		{"a sandboxed page sends Origin null", http.MethodPost, []string{"Origin", "null"}, 403},
		{"deletes are covered", http.MethodDelete, []string{"Origin", "http://evil.example"}, 403},
		{"puts are covered", http.MethodPut, []string{"Sec-Fetch-Site", "cross-site"}, 403},
		{"reads are left to the browser's own cross-origin rules", http.MethodGet, []string{"Sec-Fetch-Site", "cross-site"}, 200},
	} {
		target := "/__admin/reset"
		if tt.method == http.MethodGet {
			target = "/__admin/version"
		}
		if tt.method == http.MethodDelete || tt.method == http.MethodPut {
			target = "/__admin/mappings/aaaaaaaa-0000-0000-0000-000000000001"
		}
		rec := send(t, rt, tt.method, target, tt.hdr...)
		if tt.want == 403 {
			if rec.Code != 403 || !strings.Contains(rec.Body.String(), "forbidden") {
				t.Errorf("%s: %s %s = %d %s, want 403 forbidden", tt.name, tt.method, target, rec.Code, rec.Body)
			}
			continue
		}
		if rec.Code == 403 {
			t.Errorf("%s: %s %s was refused: %s", tt.name, tt.method, target, rec.Body)
		}
	}
}

// With the admin API on its own listener, nothing on the mock listener may reach it, however the
// path is written.
func TestAdminIsNotReachableOnTheMockListener(t *testing.T) {
	t.Parallel()
	mockOnly, _ := newRouter(t)
	mockOnly.ServeAdmin = false
	for _, target := range []string{
		"/__admin/version", "//__admin/version", "/./__admin/version", "/x/../__admin/version",
		"/%5F%5Fadmin/version", "/__admin%2Fversion", "/__ADMIN/version", "/__admin./version",
		"/__admin/../__admin/version", "/local/__admin/version", "/d/abc/__admin/version",
		"/__admin/version/", "/%2e%2e/__admin/version",
	} {
		rec := send(t, mockOnly, http.MethodGet, target)
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "version") {
			t.Errorf("GET %s reached the admin API on the mock listener", target)
		}
	}
}

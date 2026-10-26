package contract_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/test/contract"
)

// TestValidatingTransport drives the transport against a fake engine served under an org
// prefix, as in SaaS, and checks it records violations while returning real responses.
func TestValidatingTransport(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /acme/__admin/counters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"counters":[{"stubMappingId":"11111111-1111-4111-8111-111111111111","count":2,"stubLayer":"global"}]}`)
	})
	mux.HandleFunc("GET /acme/__admin/sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"sessions":[{"id":"x","kind":"dedicated"}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	base, err := url.Parse(srv.URL + "/acme")
	if err != nil {
		t.Fatal(err)
	}
	tr := &contract.ValidatingTransport{Spec: s, Base: base}
	client := &http.Client{Transport: tr}

	get := func(path string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base.String()+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	if body := get("/__admin/counters"); !strings.Contains(body, `"count":2`) {
		t.Errorf("response body was not passed through: %s", body)
	}
	if v := tr.TakeViolations(); len(v) != 0 {
		t.Errorf("valid counters response: unexpected violations %v", v)
	}

	get("/__admin/sessions")
	v := tr.TakeViolations()
	if len(v) == 0 {
		t.Fatal("invalid sessions response: want a violation, got none")
	}
	if !strings.Contains(v[0], "listSessions") {
		t.Errorf("violation should name the operation: %q", v[0])
	}
	if again := tr.TakeViolations(); len(again) != 0 {
		t.Errorf("TakeViolations should clear: %v", again)
	}
}

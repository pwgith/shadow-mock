package console_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/console"
	"github.com/pwgith/shadow-mock/test/contract"
)

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody))
	return rec
}

func TestThePageIsToldWhereTheAdminAPIIs(t *testing.T) {
	for _, base := range []string{"/__admin", "/api/v2", "/acme/__admin"} {
		r := get(t, console.Handler(base), http.MethodGet, console.Prefix+"/")
		page := r.Body.String()
		if r.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", base, r.Code)
		}
		if !strings.Contains(page, `<meta name="admin-base" content="`+base+`">`) {
			t.Errorf("%s: the page does not carry the admin base:\n%s", base, page)
		}
		if strings.Contains(page, "__ADMIN_BASE__") {
			t.Errorf("%s: the placeholder was left in the page", base)
		}
		if ct := r.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want HTML", ct)
		}
	}
}

func TestIndexHTMLIsTheSamePage(t *testing.T) {
	h := console.Handler("/__admin")
	if a, b := get(t, h, http.MethodGet, console.Prefix+"/").Body.String(), get(t, h, http.MethodGet, console.Prefix+"/index.html").Body.String(); a != b {
		t.Error("/__console/index.html differs from /__console/")
	}
}

func TestEveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	h := console.Handler("/__admin")
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	}
	for _, target := range []string{"/", "/app.js", "/style.css", "/calls.json", "/missing.js"} {
		r := get(t, h, http.MethodGet, console.Prefix+target)
		for k, v := range want {
			if k == "Cache-Control" && r.Code == http.StatusNotFound {
				continue // net/http drops it from its own error answers
			}
			if got := r.Header().Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", target, k, got, v)
			}
		}
		csp := r.Header().Get("Content-Security-Policy")
		for _, part := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, part) {
				t.Errorf("%s: Content-Security-Policy %q lacks %q", target, csp, part)
			}
		}
		if strings.Contains(csp, "unsafe-") {
			t.Errorf("%s: Content-Security-Policy allows unsafe code: %q", target, csp)
		}
	}
}

func TestTheAssetsAreServed(t *testing.T) {
	h := console.Handler("/__admin")
	for target, wantType := range map[string]string{
		"/app.js": "javascript", "/style.css": "text/css", "/calls.json": "application/json",
	} {
		r := get(t, h, http.MethodGet, console.Prefix+target)
		if r.Code != http.StatusOK || !strings.Contains(r.Header().Get("Content-Type"), wantType) {
			t.Errorf("%s: status %d, Content-Type %q, want 200 and %q", target, r.Code, r.Header().Get("Content-Type"), wantType)
		}
	}
}

func TestOnlyTheConsolesOwnFilesAreServed(t *testing.T) {
	h := console.Handler("/__admin")
	for _, target := range []string{"/missing.js", "/assets/", "/assets/app.js", "/%2e%2e/console.go", "/../console.go", "/must.go"} {
		if r := get(t, h, http.MethodGet, console.Prefix+target); r.Code == http.StatusOK {
			t.Errorf("%s answered 200", target)
		}
	}
}

func TestThePrefixWithoutASlashRedirects(t *testing.T) {
	r := get(t, console.Handler("/__admin"), http.MethodGet, console.Prefix)
	if r.Code != http.StatusMovedPermanently || r.Header().Get("Location") != console.Prefix+"/" {
		t.Errorf("status %d, Location %q, want a redirect to %s/", r.Code, r.Header().Get("Location"), console.Prefix)
	}
}

func TestTheConsoleIsReadOnly(t *testing.T) {
	h := console.Handler("/__admin")
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := get(t, h, m, console.Prefix+"/")
		if r.Code != http.StatusMethodNotAllowed || r.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: status %d, Allow %q, want 405 and \"GET, HEAD\"", m, r.Code, r.Header().Get("Allow"))
		}
	}
	if r := get(t, h, http.MethodHead, console.Prefix+"/"); r.Code != http.StatusOK || r.Body.String() != "" {
		t.Errorf("HEAD: status %d, want 200 with no body", r.Code)
	}
}

// refs matches the src and href attributes of the page.
var refs = regexp.MustCompile(`(?:src|href)="([^"]*)"`)

func TestThePageLoadsOnlyItsOwnFiles(t *testing.T) {
	page, err := console.Page()
	if err != nil {
		t.Fatal(err)
	}
	found := refs.FindAllStringSubmatch(string(page), -1)
	if len(found) == 0 {
		t.Fatal("the page references no script or style")
	}
	for _, m := range found {
		if strings.Contains(m[1], "://") || strings.HasPrefix(m[1], "//") || strings.HasPrefix(m[1], "/") {
			t.Errorf("the page loads %q, which is not a file next to it", m[1])
		}
	}
	if strings.Contains(string(page), "<script>") || regexp.MustCompile(`\son[a-z]+=`).Match(page) {
		t.Error("the page has inline script, which its Content-Security-Policy refuses")
	}
}

func TestTheScriptBuildsThePageFromTextOnly(t *testing.T) {
	script, err := console.Script()
	if err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "setTimeout(\""} {
		if strings.Contains(string(script), unsafe) {
			t.Errorf("app.js uses %s, so an answer of the API could run script in the console", unsafe)
		}
	}
}

func TestTheScriptMakesOnlyTheCallsItLists(t *testing.T) {
	script, err := console.Script()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := console.Calls()
	if err != nil {
		t.Fatal(err)
	}
	var calls map[string][]string
	if err = json.Unmarshal(raw, &calls); err != nil {
		t.Fatal(err)
	}
	src := string(script)
	used := regexp.MustCompile(`call\("([A-Za-z]+)"`).FindAllStringSubmatch(src, -1)
	named := len(used)
	for _, m := range regexp.MustCompile(`call\(pick\(.*?, "([A-Za-z]+)", "([A-Za-z]+)"\)`).FindAllStringSubmatch(src, -1) {
		used = append(used, m[:2], []string{m[0], m[2]})
		named++
	}
	if len(used) == 0 {
		t.Fatal("app.js makes no calls")
	}
	// Every use of call( but its definition names its call in one of the two forms above, so none escapes the check.
	if all := len(regexp.MustCompile(`\bcall\(`).FindAllString(src, -1)) - 1; all != named {
		t.Errorf("app.js has %d uses of call(, and %d of them name the call as a string or through pick()", all, named)
	}
	for _, m := range used {
		if _, ok := calls[m[1]]; !ok {
			t.Errorf("app.js makes the call %q, which calls.json does not list", m[1])
		}
	}
	for name := range calls {
		if !strings.Contains(string(script), `"`+name+`"`) {
			t.Errorf("calls.json lists %q, which app.js never makes", name)
		}
	}
	if n := len(regexp.MustCompile(`fetch\(`).FindAllString(string(script), -1)); n != 2 {
		t.Errorf("app.js calls fetch %d times, want 2 (an admin call and calls.json): any other call escapes calls.json", n)
	}
}

func TestEveryCallTheConsoleMakesIsInTheAPIsDescription(t *testing.T) {
	spec, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := console.Calls()
	if err != nil {
		t.Fatal(err)
	}
	var calls map[string][]string
	if err = json.Unmarshal(raw, &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls) == 0 {
		t.Fatal("calls.json lists no call")
	}
	for name, c := range calls {
		if len(c) != 2 {
			t.Errorf("%s: want [method, path], got %v", name, c)
			continue
		}
		path, _, _ := strings.Cut(c[1], "?")
		path = "/__admin" + strings.NewReplacer("{id}", "S1", "{stub}", "1d8e64c2-5a70-4a46-9b7d-0f0e2f6a1b11").Replace(path)
		if spec.FindOperation(c[0], path) == nil {
			t.Errorf("%s: %s %s is not in the API's description", name, c[0], path)
		}
	}
}

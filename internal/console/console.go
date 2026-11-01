// Package console serves the embedded web console (FR-11.17): a static single-page app that calls only the admin API.
// It holds no behaviour of its own. Everything it shows comes from an answer of the admin API, and the calls it makes
// are listed in assets/calls.json, which the tests check against the API's description.
//
// The console is for standalone and central-server use. Managed mode serves no pages (FR-11.16) and refuses to start
// with the console enabled.
package console

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed assets
var assets embed.FS

// Prefix is where the console is served.
const Prefix = "/__console"

// contentSecurityPolicy lets the page load only its own script and style, and call only its own origin.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; " +
	"img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Calls is the list of the admin calls the console makes, as embedded in the binary.
func Calls() ([]byte, error) { return assets.ReadFile("assets/calls.json") }

// Script is the console's script, for the tests that read it.
func Script() ([]byte, error) { return assets.ReadFile("assets/app.js") }

// Page is the console's page, for the tests that read it.
func Page() ([]byte, error) { return assets.ReadFile("assets/index.html") }

// Handler serves the console. adminBase is the path the admin API is served under, such as "/__admin"; the page is
// told it in a meta tag, since an operator can change it.
func Handler(adminBase string) http.Handler {
	sub := must(fs.Sub(assets, "assets"))
	page := []byte(strings.ReplaceAll(string(must(fs.ReadFile(sub, "index.html"))), "__ADMIN_BASE__", adminBase))
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "The console is read-only.", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		rest := strings.TrimPrefix(r.URL.Path, Prefix)
		switch rest {
		case "":
			http.Redirect(w, r, Prefix+"/", http.StatusMovedPermanently)
		case "/", "/index.html":
			h.Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page))
		default:
			r2 := r.Clone(r.Context())
			r2.URL.Path = rest
			files.ServeHTTP(w, r2)
		}
	})
}

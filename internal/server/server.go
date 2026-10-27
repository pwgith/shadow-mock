// Package server routes HTTP traffic (DESIGN.md §8): health endpoints, the org segment, the
// IP allowlist, the admin API and mock traffic, including /d/<id> URL-based shadow mocks.
package server

import (
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/admin"
	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Router is the engine's HTTP handler.
type Router struct {
	Directory tenant.Directory
	// RequireOrgSegment is true in SaaS: every path starts with /<org-handle>. In standalone
	// the segment is optional, and if present must be the local org's handle.
	RequireOrgSegment bool
	// LocalOrg is the org used when there is no org segment (standalone).
	LocalOrg string
	// AdminPrefix is where the admin API is served, /__admin by default (FR-6.4).
	AdminPrefix string
	Admin       *admin.Handler
	Service     *mock.Service
	Stats       *stats.Tracker
	ClientIP    ClientIP
	// ServeAdmin and ServeMock choose what this listener serves (NFR-5.2).
	ServeAdmin bool
	ServeMock  bool
	// Metrics writes Prometheus metrics; nil hides /__metrics.
	Metrics http.Handler
	// MetricsAuth, when set, requires a viewer token to read the metrics.
	MetricsAuth auth.Authenticator
	// AdminHosts, when not empty, limits the Host header the admin API and the metrics answer to.
	AdminHosts []string
	// AdminOrigins are web pages allowed to make admin changes from a browser.
	AdminOrigins []string
}

var _ http.Handler = (*Router)(nil)

// ServeHTTP implements http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	switch path {
	case "/__health":
		rt.health(w, nil)
		return
	case "/__ready":
		rt.health(w, map[string]string{"storage": "up"})
		return
	case "/__metrics":
		if rt.Metrics == nil {
			apierr.New(http.StatusNotFound, apierr.NotFound, "Metrics are not exposed here.").Write(w)
			return
		}
		if !rt.hostAllowed(w, r) || !rt.metricsAllowed(w, r) {
			return
		}
		rt.Metrics.ServeHTTP(w, r)
		return
	}
	org, prefix, rest, ok := rt.org(path)
	if !ok {
		apierr.New(http.StatusNotFound, apierr.UnknownOrg, "No org has the handle in this URL.").Write(w)
		return
	}
	adminPath, isAdmin := rt.adminPath(rest)
	if adminPath == "/__admin/health" {
		rt.health(w, nil)
		return
	}
	if (isAdmin && !rt.ServeAdmin) || (!isAdmin && !rt.ServeMock) {
		apierr.New(http.StatusNotFound, apierr.NotFound, "This listener does not serve %s.", rest).Write(w)
		return
	}
	ip := rt.ClientIP.From(r)
	// The allowlist runs before the body is read, so refused callers cost almost nothing.
	if (!isAdmin || org.AllowlistAdmin) && !org.Allows(ip) {
		rt.Stats.RecordIPRejection(org.ID, ip.String())
		e := apierr.New(http.StatusForbidden, apierr.IPNotAllowed, "Requests from %s are not allowed for this org.", ip)
		e.Details = map[string]any{"ip": ip.String()}
		e.Write(w)
		return
	}
	if isAdmin && !rt.adminGate(w, r) {
		return
	}
	ctx := tenant.With(r.Context(), org.ID)
	if isAdmin {
		r2 := r.Clone(admin.WithBasePath(ctx, prefix))
		setPath(r2.URL, adminPath)
		rt.Admin.ServeHTTP(w, r2)
		return
	}
	tr := mock.Traffic{Tenant: org.ID, Path: rest, ClientIP: ip.String(), AbsoluteURL: absoluteURL(r)}
	if id, inner, isURLBased := urlBased(rest); isURLBased {
		tr.URLMockID, tr.Path = id, inner
	}
	rt.Service.ServeMock(w, r.WithContext(ctx), tr)
}

// org resolves the org segment. It returns the org, the prefix it occupied, and the rest.
func (rt *Router) org(path string) (*tenant.Org, string, string, bool) {
	seg, rest := firstSegment(path)
	if o, found := rt.Directory.Lookup(seg); found && seg != "" {
		return o, "/" + seg, rest, true
	}
	if rt.RequireOrgSegment {
		return nil, "", "", false
	}
	o, found := rt.Directory.Lookup(rt.LocalOrg)
	return o, "", path, found
}

// firstSegment splits "/a/b/c" into "a" and "/b/c".
func firstSegment(path string) (string, string) {
	trimmed := strings.TrimPrefix(path, "/")
	seg, rest, found := strings.Cut(trimmed, "/")
	if !found {
		return seg, "/"
	}
	return seg, "/" + rest
}

// urlBased recognises /d/<id>/… and returns the id and the path after it.
func urlBased(path string) (string, string, bool) {
	rest, ok := strings.CutPrefix(path, "/d/")
	if !ok {
		return "", "", false
	}
	id, inner, found := strings.Cut(rest, "/")
	if id == "" {
		return "", "", false
	}
	if !found {
		return id, "/", true
	}
	return id, "/" + inner, true
}

func setPath(u *url.URL, escaped string) {
	if p, err := url.PathUnescape(escaped); err == nil {
		u.Path = p
	} else {
		u.Path = escaped
	}
	u.RawPath = escaped
}

func absoluteURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.RequestURI()
}

func (rt *Router) health(w http.ResponseWriter, checks map[string]string) {
	apierr.WriteJSON(w, http.StatusOK, rt.Admin.Health(checks))
}

// New builds an HTTP server with the timeouts the coding standard requires (§12).
func New(addr string, h http.Handler, maxDelay time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      maxDelay + 60*time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// adminPath maps a path under the configured admin prefix to the /__admin path the admin
// API routes on.
func (rt *Router) adminPath(rest string) (string, bool) {
	prefix := rt.AdminPrefix
	if prefix == "" {
		prefix = "/__admin"
	}
	if rest != prefix && !strings.HasPrefix(rest, prefix+"/") {
		return "", false
	}
	return "/__admin" + strings.TrimPrefix(rest, prefix), true
}

// crossSite reports whether a browser sent r from a page on another site. Browsers add Origin
// and Sec-Fetch-Site to such requests, and scripts cannot remove them, so a web page cannot
// use a visitor's browser to call an admin API that has no login. Command-line and library
// clients send neither header and are not affected.
func crossSite(r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin != "" && slices.ContainsFunc(allowed, func(o string) bool { return strings.EqualFold(strings.TrimRight(o, "/"), origin) }) {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return true
	}
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err != nil || !strings.EqualFold(u.Host, r.Host)
}

// hostAllowed refuses a request whose Host header is not one of AdminHosts. A web page reaches a
// server on a private address through DNS rebinding, which changes what a name points to but not
// the name the browser sends, so the name is what is checked.
func (rt *Router) hostAllowed(w http.ResponseWriter, r *http.Request) bool {
	if len(rt.AdminHosts) == 0 {
		return true
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if slices.ContainsFunc(rt.AdminHosts, func(a string) bool { return strings.EqualFold(a, host) }) {
		return true
	}
	apierr.New(http.StatusForbidden, apierr.Forbidden, "The admin API does not answer to the host name %q.", host).Write(w)
	return false
}

// metricsAllowed requires a viewer token for the metrics when the admin API has tokens.
func (rt *Router) metricsAllowed(w http.ResponseWriter, r *http.Request) bool {
	if rt.MetricsAuth == nil {
		return true
	}
	p, err := rt.MetricsAuth.Authenticate(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="shadowmock"`)
		apierr.WriteError(w, err)
		return false
	}
	if !p.Has(auth.Viewer) {
		apierr.New(http.StatusForbidden, apierr.Forbidden, "The metrics need a token with the viewer role.").Write(w)
		return false
	}
	return true
}

// adminGate applies the checks that guard the admin API against web pages: the Host header, and for
// changes the page that sent them. It writes the refusal and returns false when one fails.
func (rt *Router) adminGate(w http.ResponseWriter, r *http.Request) bool {
	if !rt.hostAllowed(w, r) {
		return false
	}
	change := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
	if change && crossSite(r, rt.AdminOrigins) {
		apierr.New(http.StatusForbidden, apierr.Forbidden, "The admin API does not accept changes sent from a web page on another site.").Write(w)
		return false
	}
	return true
}

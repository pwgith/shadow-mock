// Package server routes HTTP traffic (DESIGN.md §8): health endpoints, the org segment, the
// IP allowlist, the admin API and mock traffic, including /d/<id> URL-based shadow mocks.
package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/admin"
	"github.com/pwgith/shadow-mock/internal/apierr"
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
	Admin    *admin.Handler
	Service  *mock.Service
	Stats    *stats.Tracker
	ClientIP ClientIP
	// ServeAdmin and ServeMock choose what this listener serves (NFR-5.2).
	ServeAdmin bool
	ServeMock  bool
	// Metrics writes Prometheus metrics; nil hides /__metrics.
	Metrics func(w http.ResponseWriter)
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
		rt.Metrics(w)
		return
	}
	org, prefix, rest, ok := rt.org(path)
	if !ok {
		apierr.New(http.StatusNotFound, apierr.UnknownOrg, "No org has the handle in this URL.").Write(w)
		return
	}
	isAdmin := rest == "/__admin" || strings.HasPrefix(rest, "/__admin/")
	if rest == "/__admin/health" {
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
	ctx := tenant.With(r.Context(), org.ID)
	if isAdmin {
		r2 := r.Clone(admin.WithBasePath(ctx, prefix))
		setPath(r2.URL, rest)
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

// WriteMetrics writes a minimal Prometheus exposition. Full metrics arrive in P4.
func WriteMetrics(svc *mock.Service) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		sessions := len(svc.Sessions.All())
		fmt.Fprintf(w, "# HELP shadowmock_active_sessions Active key-based and URL-based shadow mocks.\n")
		fmt.Fprintf(w, "# TYPE shadowmock_active_sessions gauge\nshadowmock_active_sessions %d\n", sessions)
	}
}

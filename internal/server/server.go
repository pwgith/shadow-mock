// Package server routes HTTP traffic (DESIGN.md §8): health endpoints, the org segment, the
// IP allowlist, the admin API and mock traffic, including /d/<id> URL-based shadow mocks.
package server

import (
	"context"
	"maps"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/pwgith/shadow-mock/internal/admin"
	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/browserproxy"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/ratelimit"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/telemetry"
	"github.com/pwgith/shadow-mock/internal/tenant"
	"github.com/pwgith/shadow-mock/internal/usage"
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
	// Telemetry traces admin requests; nil records nothing.
	Telemetry *telemetry.Telemetry
	// Limiter, when set, limits how fast each client address may send mock traffic.
	Limiter *ratelimit.Limiter
	// AdminRequireHTTPS refuses admin calls that did not arrive over TLS (--admin-api-require-https).
	AdminRequireHTTPS bool
	// BrowserProxy, when set, lets clients use this listener as a proxy server: CONNECT tunnels are
	// opened up, and requests in absolute form are answered as mock traffic that falls through to the
	// origin when no mapping matches (--enable-browser-proxying).
	BrowserProxy *browserproxy.Proxy
	// Managed mode (FR-11): Usage counts what each org uses, Clock drives the per-org limiter, and
	// ExemptAdminTokens lets a token from a trusted issuer past the admin allowlist.
	Usage             *usage.Meter
	Clock             clock.Clock
	ExemptAdminTokens bool
	// OrgLimits holds each org's rate limiter; nil means no per-org limit.
	OrgLimits *OrgLimiters
	// Checks reports the shared stores for /__ready, each as "up" or "down". Nil reports none.
	Checks func(context.Context) map[string]string
}

// OrgLimiters are the per-org rate limiters, shared by every Router made from the same configuration.
type OrgLimiters struct{ m sync.Map } // tenant.ID -> *orgLimiter

// NewOrgLimiters returns an empty set.
func NewOrgLimiters() *OrgLimiters { return &OrgLimiters{} }

var _ http.Handler = (*Router)(nil)

// ServeHTTP implements http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		rt.connect(w, r)
		return
	}
	path := r.URL.EscapedPath()
	if rt.serveProbe(w, r, path) {
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
	if rt.refuseOrgState(w, org, isAdmin, r.Method, adminPath) {
		return
	}
	ip := rt.ClientIP.From(r)
	switch rt.allowlistVerdict(org, ip, isAdmin) {
	case refuse:
		rt.rejectIP(w, org, ip)
		return
	case admitTokens:
		r = r.WithContext(admin.WithIPDenied(r.Context(), ip))
	case admit:
	}
	if isAdmin && rt.AdminRequireHTTPS && r.TLS == nil {
		(&apierr.ValidationError{Status: http.StatusForbidden, Errors: []apierr.WireMockError{{Code: 40, Title: "HTTPS is required for accessing the admin API"}}}).Write(w)
		return
	}
	if isAdmin && !rt.adminGate(w, r) {
		return
	}
	if !isAdmin && !rt.admitMock(w, org, ip) {
		return
	}
	ctx := tenant.With(auth.WithClientIP(r.Context(), ip), org.ID) // the key-usage record needs the resolved address
	if isAdmin {
		r2 := r.Clone(admin.WithBasePath(ctx, prefix))
		setPath(r2.URL, adminPath)
		rt.serveAdmin(w, r2)
		return
	}
	tr := mock.Traffic{Tenant: org.ID, Path: rest, ClientIP: ip.String(), AbsoluteURL: absoluteURL(r), BrowserProxy: rt.viaProxy(r)}
	if id, inner, isURLBased := urlBased(rest); isURLBased {
		tr.URLMockID, tr.Path = id, inner
	}
	rt.Service.ServeMock(w, r.WithContext(ctx), tr) //nolint:contextcheck // the request context reaches the database, Redis and object-store drivers through interfaces; the analyser takes their internal contexts for new ones
}

// serveProbe answers the health, readiness and metrics paths, which belong to no org. It reports whether it did.
func (rt *Router) serveProbe(w http.ResponseWriter, r *http.Request, path string) bool {
	switch path {
	case "/__health":
		rt.health(w, nil)
	case "/__ready":
		rt.ready(w, r)
	case "/__metrics":
		rt.serveMetrics(w, r)
	default:
		return false
	}
	return true
}

type verdict int

const (
	// admit lets the request on.
	admit verdict = iota
	// refuse turns it away.
	refuse
	// admitTokens lets an admin call on far enough for the admin handler to look at its credential: the caller is
	// outside the allowlist, and only a token from the trusted issuer may pass.
	admitTokens
)

// allowlistVerdict applies the org's allowlist before the body is read, so refused callers cost almost nothing. A
// caller outside it is refused, except for an admin call when tokens from the trusted issuer are let past: the
// management app calls from the host's servers, not the customer's networks, so the admin handler decides once it
// knows the credential.
func (rt *Router) allowlistVerdict(org *tenant.Org, ip netip.Addr, isAdmin bool) verdict {
	switch {
	case (isAdmin && !org.AllowlistAdmin) || org.Allows(ip):
		return admit
	case isAdmin && rt.ExemptAdminTokens:
		return admitTokens
	default:
		return refuse
	}
}

// ready answers /__ready: the engine's own storage, and each shared store when there is one. A store that is down
// makes the status degraded; the engine keeps serving what it has (FR-11.14).
func (rt *Router) ready(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{"storage": "up"}
	if rt.Checks != nil {
		maps.Copy(checks, rt.Checks(r.Context()))
	}
	h := rt.Admin.Health(checks)
	for _, v := range checks {
		if v != "up" {
			h.Status = "degraded"
		}
	}
	apierr.WriteJSON(w, http.StatusOK, h)
}

// viaProxy reports whether a request reached this listener as a proxy request: sent in absolute form,
// or inside a CONNECT tunnel.
func (rt *Router) viaProxy(r *http.Request) bool {
	if rt.BrowserProxy == nil {
		return false
	}
	_, tunnelled := browserproxy.Authority(r.Context())
	return tunnelled || r.URL.IsAbs()
}

// connect opens a tunnel for a client that wants this listener as its proxy server. The tunnel is
// subject to the same allowlist and rate limit as any mock traffic, in the local org.
func (rt *Router) connect(w http.ResponseWriter, r *http.Request) {
	if rt.BrowserProxy == nil || !rt.ServeMock || rt.RequireOrgSegment {
		apierr.New(http.StatusMethodNotAllowed, apierr.NotFound, "Browser proxying is not turned on.").Write(w)
		return
	}
	org, found := rt.Directory.Lookup(rt.LocalOrg)
	if !found {
		apierr.New(http.StatusNotFound, apierr.UnknownOrg, "No org has the handle in this URL.").Write(w)
		return
	}
	ip := rt.ClientIP.From(r)
	if !org.Allows(ip) {
		rt.Stats.RecordIPRejection(org.ID, ip.String())
		apierr.New(http.StatusForbidden, apierr.IPNotAllowed, "Requests from %s are not allowed for this org.", ip).Write(w)
		return
	}
	if !rt.withinRate(w, ip) {
		return
	}
	rt.BrowserProxy.Serve(w, r, rt)
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
		w.Header().Set("WWW-Authenticate", auth.Challenge(rt.MetricsAuth))
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

// statusWriter remembers the status a handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader remembers the status and passes it on.
func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// serveAdmin runs the admin API inside a span that continues the caller's trace. The span names
// the method and the status only; the path can carry a session id, which is not a span name.
func (rt *Router) serveAdmin(w http.ResponseWriter, r *http.Request) {
	ctx, span := rt.Telemetry.Start(r, "admin "+r.Method)
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	rt.Admin.ServeHTTP(sw, r.WithContext(ctx)) //nolint:contextcheck // see ServeMock in ServeHTTP
	span.SetAttributes(attribute.String("http.request.method", r.Method), attribute.Int("http.response.status_code", sw.status), attribute.String("shadowmock.kind", "admin"))
	if sw.status >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(sw.status))
	}
	span.End()
}

// rejectIP answers a request the allowlist refuses, and counts it.
func (rt *Router) rejectIP(w http.ResponseWriter, org *tenant.Org, ip netip.Addr) {
	rt.Stats.RecordIPRejection(org.ID, ip.String())
	if rt.Usage != nil {
		rt.Usage.IPRejected(org.ID)
	}
	e := apierr.New(http.StatusForbidden, apierr.IPNotAllowed, "Requests from %s are not allowed for this org.", ip)
	e.Details = map[string]any{"ip": ip.String()}
	e.Write(w)
}

// refuseOrgState answers for an org that is not served (FR-11.5): suspended and cancelled orgs get 403
// and say why; a deleted one gets 410 for everything but the management app's purge. It reports whether
// it answered.
func (rt *Router) refuseOrgState(w http.ResponseWriter, org *tenant.Org, isAdmin bool, method, adminPath string) bool {
	switch {
	case org.Status.Serves():
		return false
	case org.Status == tenant.StatusDeleted:
		if isAdmin && method == http.MethodPost && adminPath == "/__admin/org/purge" {
			return false
		}
		w.Header().Set(ReasonHeader, string(apierr.OrgDeleted))
		apierr.New(http.StatusGone, apierr.OrgDeleted, "This org has been deleted.").Write(w)
	default:
		w.Header().Set(ReasonHeader, string(apierr.OrgSuspended))
		apierr.New(http.StatusForbidden, apierr.OrgSuspended, "This org is suspended.").Write(w)
	}
	return true
}

// ReasonHeader names the header that says why the engine refused a request.
const ReasonHeader = apierr.ReasonHeader

// LimitHeader names the header that says which limit applied to a rate-limited request.
const LimitHeader = "X-Shadow-Mock-Limit"

// orgLimiter is an org's rate limiter and the limits it was made with, so a plan change makes a new one.
type orgLimiter struct {
	limits tenant.Limits
	l      *ratelimit.Limiter
}

// admitMock lets a mock request in, or answers it: the client's own rate, the org's rate, and the org's
// monthly cap (FR-11.5, FR-11.6). A request let in is counted as served.
func (rt *Router) admitMock(w http.ResponseWriter, org *tenant.Org, ip netip.Addr) bool {
	if !rt.withinRate(w, ip) {
		if rt.Usage != nil {
			rt.Usage.RateLimited(org.ID)
		}
		return false
	}
	if !rt.withinOrgRate(w, org) || !rt.withinCap(w, org) {
		return false
	}
	if rt.Usage != nil {
		rt.Usage.Request(org.ID)
	}
	return true
}

// withinOrgRate takes a token from the org's bucket. An org over its rate gets 429, Retry-After and the
// name of the limit, before its body is read, and other orgs are not affected.
func (rt *Router) withinOrgRate(w http.ResponseWriter, org *tenant.Org) bool {
	lim := org.Limits
	if lim.RequestsPerSecond <= 0 || rt.Clock == nil || rt.OrgLimits == nil {
		return true
	}
	cur, _ := rt.OrgLimits.m.Load(org.ID)
	ol, found := cur.(*orgLimiter)
	if !found || ol.limits != lim {
		ol = &orgLimiter{limits: lim, l: ratelimit.New(rt.Clock, lim.RequestsPerSecond, lim.Burst)}
		rt.OrgLimits.m.Store(org.ID, ol)
	}
	ok, wait := ol.l.Allow(string(org.ID))
	if ok {
		return true
	}
	seconds := max(int(math.Ceil(wait.Seconds())), 1)
	if rt.Usage != nil {
		rt.Usage.RateLimited(org.ID)
	}
	rt.Stats.RecordRateLimited(org.ID)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set(LimitHeader, "requests-per-second")
	e := apierr.New(http.StatusTooManyRequests, apierr.RateLimited, "This org is over its limit of %g requests a second; try again in %d s.", lim.RequestsPerSecond, seconds)
	e.Details = map[string]any{"retryAfterSeconds": seconds, "limit": "requests-per-second"}
	e.Write(w)
	return false
}

// withinCap refuses mock traffic once the org has used its monthly cap and the plan's grace.
func (rt *Router) withinCap(w http.ResponseWriter, org *tenant.Org) bool {
	lim := org.Limits
	if lim.MonthlyRequestCap <= 0 || rt.Usage == nil || rt.Clock == nil {
		return true
	}
	if rt.Usage.Month(org.ID, rt.Clock.Now()) < lim.MonthlyRequestCap+lim.Grace {
		return true
	}
	w.Header().Set(ReasonHeader, string(apierr.MonthlyRequestCap))
	apierr.New(http.StatusTooManyRequests, apierr.MonthlyRequestCap, "This org has used its %d requests for the month.", lim.MonthlyRequestCap).Write(w)
	return false
}

// withinRate takes a token for the client from the rate limiter. A client over its rate gets 429 and
// a Retry-After header, before any body is read.
func (rt *Router) withinRate(w http.ResponseWriter, ip netip.Addr) bool {
	if rt.Limiter == nil {
		return true
	}
	ok, wait := rt.Limiter.Allow(ip.String())
	if ok {
		return true
	}
	seconds := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(seconds, 1)))
	e := apierr.New(http.StatusTooManyRequests, apierr.RateLimited, "Too many requests from %s; try again in %d s.", ip, max(seconds, 1))
	e.Details = map[string]any{"retryAfterSeconds": max(seconds, 1)}
	e.Write(w)
	return false
}

// serveMetrics serves /__metrics to a caller that passes the host and token checks.
func (rt *Router) serveMetrics(w http.ResponseWriter, r *http.Request) {
	if rt.Metrics == nil {
		apierr.New(http.StatusNotFound, apierr.NotFound, "Metrics are not exposed here.").Write(w)
		return
	}
	if !rt.hostAllowed(w, r) || !rt.metricsAllowed(w, r) {
		return
	}
	rt.Metrics.ServeHTTP(w, r)
}

// Protocols says which HTTP versions a server speaks (FR-10.14): HTTP/1.1 always, HTTP/2 over TLS
// through ALPN, and HTTP/2 without TLS on prior knowledge (h2c), each unless turned off.
func Protocols(srv *http.Server, http2TLS, http2Plain bool) {
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetHTTP2(http2TLS)
	p.SetUnencryptedHTTP2(http2Plain)
	srv.Protocols = &p
}

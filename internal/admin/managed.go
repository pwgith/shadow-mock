package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/audit"
	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/usage"
)

// Managed-mode parts of the admin API (FR-11): the metadata scope, the audit trail, purge and anonymise,
// usage and the history of the stats.

type ipDeniedKey struct{}

// WithIPDenied records that the allowlist would refuse the caller's address. The router does not refuse
// it itself on an admin path in managed mode, because a token from the trusted issuer is exempt: the
// management app calls from the host's servers, not the customer's networks (FR-9.6b).
func WithIPDenied(ctx context.Context, ip netip.Addr) context.Context {
	return context.WithValue(ctx, ipDeniedKey{}, ip)
}

func ipDenied(ctx context.Context) (netip.Addr, bool) {
	ip, ok := ctx.Value(ipDeniedKey{}).(netip.Addr)
	return ip, ok
}

// metadataRoutes are the routes a token with the metadata scope may call (FR-11.4): they say how an org is set
// up, and what they return has the content taken out. Any route not listed here is refused, so a route
// added later is closed to the scope until someone decides what it shows.
func metadataRoutes() []string {
	return []string{
		"GET /__admin/sessions",
		"GET /__admin/sessions/{sessionId}",
		"GET /__admin/requests",
		"GET /__admin/sessions/{sessionId}/requests",
		"GET /__admin/usage",
		"GET /__admin/stats",
		"GET /__admin/stats/live",
		"GET /__admin/version",
		"GET /__admin/recordings/status",
	}
}

// contentKeys are the fields of a response that hold what an org keeps: bodies, headers, cookies, query
// strings, mappings and the secrets of a shadow mock. The metadata scope removes them wherever they are.
func contentKeys() []string {
	return []string{
		"body", "bodyAsBase64", "jsonBody", "headers", "cookies", "queryParams", "formParams", "stubMapping",
		"responseDefinition", "mappings", "key", "tokens", "metadata", "absoluteUrl", "loggedRequest",
	}
}

// metaWriter holds a response back until the handler is done, so content can be taken out of it.
type metaWriter struct {
	http.ResponseWriter
	buf    bytes.Buffer
	status int
}

// WriteHeader holds the status until the content has been stripped.
func (m *metaWriter) WriteHeader(code int) { m.status = code }

// Write holds the body until the content has been stripped.
func (m *metaWriter) Write(b []byte) (int, error) { return m.buf.Write(b) }

// Unwrap lets http.ResponseController reach the real writer.
func (m *metaWriter) Unwrap() http.ResponseWriter { return m.ResponseWriter }

func newMetaWriter(w http.ResponseWriter) *metaWriter {
	return &metaWriter{ResponseWriter: w, status: http.StatusOK}
}

// flush sends the response with the content removed.
func (m *metaWriter) flush() {
	body := m.buf.Bytes()
	if m.Header().Get("Content-Type") != "" && strings.Contains(m.Header().Get("Content-Type"), "json") {
		var v any
		if json.Unmarshal(body, &v) == nil {
			if out, err := json.Marshal(stripContent(v)); err == nil {
				body = out
				m.Header().Set("Content-Length", strconv.Itoa(len(body)))
			}
		}
	}
	m.ResponseWriter.WriteHeader(m.status)
	_, _ = m.ResponseWriter.Write(body)
}

// stripContent removes content keys from decoded JSON, and reduces a "response" object to its status.
func stripContent(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			switch {
			case slices.Contains(contentKeys(), k):
				continue
			case k == "url":
				if u, ok := x.(string); ok {
					out[k], _, _ = strings.Cut(u, "?") // the path says which endpoint; the query may hold a secret
					continue
				}
			case k == "response":
				if resp, ok := x.(map[string]any); ok {
					out[k] = map[string]any{keyStatus: resp[keyStatus]}
					continue
				}
			}
			out[k] = stripContent(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = stripContent(x)
		}
		return out
	}
	return v
}

// statusWriter records the status for the audit trail.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader notes the status and passes it on.
func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// audited records a state-changing call in the audit trail once it has been answered.
func (h *Handler) audited(w http.ResponseWriter, r *http.Request, p auth.Principal, route string) (http.ResponseWriter, func()) {
	if h.cfg.Audit == nil || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return w, func() {}
	}
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	return sw, func() {
		h.cfg.Audit.Record(audit.Entry{
			At: h.svc.Clock.Now().UTC(), Org: p.OrgID, Issuer: p.Issuer, Subject: p.UserID, Key: p.KeyLabel,
			TokenID: p.TokenID, Method: r.Method, Route: route, Status: sw.status,
		})
	}
}

// listAudit answers GET /audit.
func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if h.cfg.Audit == nil {
		return apierr.New(http.StatusNotFound, apierr.NotSupported, "This server keeps no audit trail.")
	}
	entries := h.cfg.Audit.List(sc.Tenant)
	return writeJSON(w, http.StatusOK, map[string]any{"entries": page(r, entries)})
}

// purgeOrg answers POST /org/purge: it deletes the org's runtime state, and can be run again (FR-11.15).
func (h *Handler) purgeOrg(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	res, err := h.svc.PurgeOrg(r.Context(), sc.Tenant)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if res.Sessions+res.JournalEntries > 0 {
		status = http.StatusAccepted
	}
	return writeJSON(w, status, map[string]any{keyStatus: "done", "sessions": res.Sessions, "journalEntries": res.JournalEntries})
}

// anonymisePerson answers POST /people/{personId}/anonymise.
func (h *Handler) anonymisePerson(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	person := r.PathValue("personId")
	sessions := h.svc.AnonymisePerson(r.Context(), sc.Tenant, person)
	entries := 0
	if h.cfg.Audit != nil {
		entries = h.cfg.Audit.Anonymise(sc.Tenant, person)
	}
	return writeJSON(w, http.StatusOK, map[string]any{keyStatus: "done", "sessions": sessions, "auditEntries": entries})
}

// usageView is the answer to GET /usage.
type usageView struct {
	Buckets []usage.Bucket `json:"buckets"`
}

// usage answers GET /usage?from&to with the org's hourly buckets (FR-11.12).
func (h *Handler) usage(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if h.svc.Usage == nil {
		return apierr.New(http.StatusNotFound, apierr.NotSupported, "Standalone and central-server mode have no plans or usage.")
	}
	from, to, err := timeRange(r)
	if err != nil {
		return err
	}
	h.noteFiles(r.Context(), sc)
	return writeJSON(w, http.StatusOK, usageView{Buckets: h.svc.Usage.Buckets(sc.Tenant, from, to)})
}

// noteFiles records what the org stores now, so the bucket carries its peak.
func (h *Handler) noteFiles(ctx context.Context, sc store.Scope) {
	names, err := h.svc.Files.List(ctx, store.Global(sc.Tenant))
	if err != nil {
		return
	}
	var total int64
	for _, n := range names {
		if b, readErr := h.svc.Files.Read(ctx, store.Global(sc.Tenant), n); readErr == nil {
			total += int64(len(b))
		}
	}
	h.svc.Usage.StoredFiles(sc.Tenant, usage.Files{Count: len(names), Bytes: total})
}

// timeRange reads from and to as RFC 3339; both are optional.
func timeRange(r *http.Request) (from, to time.Time, err error) {
	q := r.URL.Query()
	for name, dst := range map[string]*time.Time{"from": &from, "to": &to} {
		v := q.Get(name)
		if v == "" {
			continue
		}
		t, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return from, to, badRequest("%s must be an ISO 8601 date-time, not %q.", name, v)
		}
		*dst = t
	}
	return from, to, nil
}

// historyStats answers GET /stats?from&to&step: the live stats' measures for each step of a range.
func (h *Handler) historyStats(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	from, to, err := timeRange(r)
	if err != nil {
		return err
	}
	now := h.svc.Clock.Now()
	if to.IsZero() {
		to = now
	}
	if from.IsZero() {
		from = to.Add(-time.Hour)
	}
	step := time.Hour
	if v := r.URL.Query().Get("step"); v != "" {
		if step, err = time.ParseDuration(v); err != nil || step < time.Minute {
			return badRequest("step must be a duration of at least a minute, such as 15m or 1h.")
		}
	}
	if to.Sub(from)/step > 1000 {
		return badRequest("That range and step make more than 1000 points; use a larger step.")
	}
	active := stats.ActiveSessions{
		KeyBased: len(h.svc.Sessions.List(sc.Tenant, "key-based")),
		URLBased: len(h.svc.Sessions.List(sc.Tenant, "url-based")),
	}
	return writeJSON(w, http.StatusOK, map[string]any{"points": h.svc.Stats.History(sc.Tenant, from, to, step, active)})
}

// keyStatus is the JSON key of a status.
const keyStatus = "status"

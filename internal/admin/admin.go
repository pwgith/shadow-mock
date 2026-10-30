// Package admin serves the /__admin API (api/openapi.yaml, DESIGN.md §10): the
// WireMock-compatible endpoints, their session-scoped twins, sessions and runtime extensions.
// Every request is authenticated, checked against the org in the path, and authorised before
// a handler runs, in standalone mode too.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/audit"
	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// ScopeHeader scopes a global endpoint to a session (FR-7.3).
const ScopeHeader = "X-Shadow-Mock-Key"

// Config holds the admin API's own options.
type Config struct {
	Version string
	// PublicBaseURL is the externally visible base URL for building URL-based mock base URLs.
	// Empty derives it from the request.
	PublicBaseURL string
	// MaxBodyBytes caps admin request bodies.
	MaxBodyBytes int64
	// Shutdown stops the process (standalone only). Nil disables the endpoint.
	Shutdown func()
	SaaS     bool
	// Managed is true in managed mode: no API docs pages, and a token from the trusted issuer is let past the
	// admin allowlist. Audit, when set, keeps the audit trail of state-changing calls (FR-11.15).
	Managed bool
	Audit   *audit.Trail
}

// Handler is the admin API.
type Handler struct {
	svc       *mock.Service
	authn     auth.Authenticator
	authz     auth.Authorizer
	cfg       Config
	logger    *slog.Logger
	startedAt time.Time
	mux       *http.ServeMux
	spec      specCache
}

// New builds the admin API.
func New(svc *mock.Service, authn auth.Authenticator, authz auth.Authorizer, cfg Config, logger *slog.Logger) *Handler {
	h := &Handler{
		svc: svc, authn: authn, authz: authz, cfg: cfg, logger: logger,
		startedAt: svc.Clock.Now(), mux: http.NewServeMux(),
	}
	h.routes()
	return h
}

type ctxKey int

const (
	principalKey ctxKey = iota
	basePathKey
)

// WithBasePath records the org prefix the request arrived under (for example "/acme"), so
// responses can build absolute URLs.
func WithBasePath(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, basePathKey, p)
}

func basePath(ctx context.Context) string {
	p, _ := ctx.Value(basePathKey).(string) //nolint:errcheck // absent means no prefix
	return p
}

// ServeHTTP authenticates the caller, checks the org (FR-9.6a) and dispatches. The tenant must
// already be in the context (tenant.With).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, err := h.authn.Authenticate(r)
	if err != nil {
		var ae *apierr.Error
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", auth.Challenge(h.authn))
		}
		apierr.WriteError(w, err)
		return
	}
	t := tenant.From(r.Context())
	if p.OrgID != t {
		apierr.New(http.StatusForbidden, apierr.OrgMismatch,
			"Token is for org %s, URL is for org %s; switch active org.", p.OrgID, t).Write(w)
		return
	}
	if ip, denied := ipDenied(r.Context()); denied && p.Kind != auth.KindToken {
		h.svc.Stats.RecordIPRejection(t, ip.String())
		if h.svc.Usage != nil {
			h.svc.Usage.IPRejected(t)
		}
		e := apierr.New(http.StatusForbidden, apierr.IPNotAllowed, "Requests from %s are not allowed for this org.", ip)
		e.Details = map[string]any{"ip": ip.String()}
		e.Write(w)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
	_, route := h.mux.Handler(r)
	if p.Scope == auth.ScopeMetadata {
		if !slices.Contains(metadataRoutes(), route) {
			apierr.New(http.StatusForbidden, apierr.ScopeMetadata, "A session with the metadata scope cannot do this.").Write(w)
			return
		}
		mw := newMetaWriter(w)
		defer mw.flush()
		w = mw
	}
	w, done := h.audited(w, r, p, route)
	defer done()
	h.mux.ServeHTTP(w, r)
}

func principal(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(principalKey).(auth.Principal) //nolint:errcheck // set by ServeHTTP
	return p
}

// access is what a scoped handler does to its scope.
type access int

const (
	// readAccess reads (viewer).
	readAccess access = iota
	// stateAccess changes runtime state: the journal and call counters (editor).
	stateAccess
	// configAccess changes configuration: mappings, files, recordings. On the global scope it
	// is a global write, which SaaS rejects (FR-9.5a).
	configAccess
	// lifecycleAccess creates or lists shadow mocks on a global path: not a write to the global layer.
	lifecycleAccess
	// validateAccess checks a mapping or dry-runs a request, changing nothing.
	validateAccess
	// manageAccess is what only the management app does for itself (FR-11.15).
	manageAccess
)

func (a access) action(global bool) auth.Action {
	switch {
	case a == readAccess:
		return auth.Read
	case a == validateAccess:
		return auth.Validate
	case a == manageAccess:
		return auth.Manage
	case a == configAccess && global:
		return auth.GlobalWrite
	default:
		return auth.SessionWrite
	}
}

// scopedFunc is a handler written once against a scope (DESIGN.md §10.1).
type scopedFunc func(w http.ResponseWriter, r *http.Request, sc store.Scope) error

// pattern builds a ServeMux pattern; an empty method matches every method.
func pattern(method, path string) string {
	if method == "" {
		return path
	}
	return method + " " + path
}

// handle registers a handler on the global path and on its /__admin/sessions/{sessionId} twin.
func (h *Handler) handle(method, path string, a access, fn scopedFunc) {
	h.handleGlobal(method, path, a, fn)
	h.handleSession(method, path, a, fn)
}

// handleGlobal registers a handler on the global path only.
func (h *Handler) handleGlobal(method, path string, a access, fn scopedFunc) {
	h.mux.Handle(pattern(method, "/__admin"+path), h.scoped(a, fn, false))
}

// handleSession registers a handler on the session path only.
func (h *Handler) handleSession(method, path string, a access, fn scopedFunc) {
	h.mux.Handle(pattern(method, "/__admin/sessions/{sessionId}"+path), h.scoped(a, fn, true))
}

func (h *Handler) scoped(a access, fn scopedFunc, sessionPath bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, err := h.scope(r, sessionPath)
		if err == nil {
			err = h.authz.Authorize(r.Context(), principal(r.Context()), a.action(sc.IsGlobal()),
				auth.Resource{Tenant: sc.Tenant, Global: sc.IsGlobal(), Lifecycle: a == lifecycleAccess})
		}
		if err == nil {
			err = fn(w, r, sc)
		}
		if err != nil {
			h.writeError(r.Context(), w, err)
		}
	})
}

// scope resolves the scope from the path, or from the X-Shadow-Mock-Key header on a global path.
func (h *Handler) scope(r *http.Request, sessionPath bool) (store.Scope, error) {
	t := tenant.From(r.Context())
	id := ""
	if sessionPath {
		id = r.PathValue("sessionId")
	} else if key := r.Header.Get(ScopeHeader); key != "" {
		sess, ok := h.svc.Sessions.Resolve(t, key)
		if !ok {
			return store.Scope{}, apierr.New(http.StatusNotFound, apierr.NotFound, "No active session for the %s header.", ScopeHeader)
		}
		id = sess.ID
	}
	sc := store.Scope{Tenant: t, SessionID: id}
	if id != "" {
		if _, err := h.svc.RequireSession(sc); err != nil {
			return store.Scope{}, err
		}
	}
	return sc, nil
}

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	var ae *apierr.Error
	var ve *apierr.ValidationError
	if !errors.As(err, &ae) && !errors.As(err, &ve) {
		h.logger.ErrorContext(ctx, "admin request failed", "error", err.Error())
	}
	apierr.WriteError(w, err)
}

// decode reads a JSON body into v.
func (h *Handler) decode(r *http.Request, v any) error {
	b, err := h.readBody(r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed, "The body is not valid JSON for this endpoint: %v.", err)
	}
	return nil
}

// readBody reads a body capped at MaxBodyBytes.
func (h *Handler) readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	rd := io.Reader(r.Body)
	if h.cfg.MaxBodyBytes > 0 {
		rd = io.LimitReader(r.Body, h.cfg.MaxBodyBytes+1)
	}
	b, err := io.ReadAll(rd)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if h.cfg.MaxBodyBytes > 0 && int64(len(b)) > h.cfg.MaxBodyBytes {
		return nil, apierr.New(http.StatusRequestEntityTooLarge, apierr.BodyTooLarge, "The body exceeds %d bytes.", h.cfg.MaxBodyBytes)
	}
	return b, nil
}

// ok sends 200 with no body, as the contract documents for most write operations.
func ok(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusOK)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	apierr.WriteJSON(w, status, v)
	return nil
}

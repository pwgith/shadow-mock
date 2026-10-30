package admin

import (
	"net/http"
	"slices"
	"strings"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
)

func (h *Handler) sessionView(r *http.Request, s *session.Session) sessionView {
	sc := s.Scope()
	mappings, _ := h.svc.Mappings.List(r.Context(), sc) //nolint:errcheck // a count of 0 is a fine fallback
	v := sessionView{
		ID: s.ID, Kind: string(s.Kind), Name: s.Name, Tenant: string(s.Tenant), CreatedBy: s.CreatedBy, CreatedByKey: s.CreatedByKey,
		CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, TTLSeconds: int64(s.TTL.Seconds()),
		Masks: s.Masks, MappingCount: len(mappings), RequestCount: h.svc.Journal.Count(sc, nil), Metadata: s.Metadata,
	}
	if s.ParentID != "" {
		p := s.ParentID
		v.ParentID = &p
	}
	if s.Kind == session.KeyBased {
		v.KeyPrefix, v.Tokens = s.KeyPrefix(), s.Tokens
	} else {
		v.BaseURL = baseURLFor(h.cfg.PublicBaseURL, r, s.ID)
	}
	return v
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, sc store.Scope, kind session.Kind) error {
	var req mock.CreateSessionRequest
	if err := h.decode(r, &req); err != nil {
		return err
	}
	req.CreatedByKey = principal(r.Context()).KeyLabel
	s, err := h.svc.CreateSession(r.Context(), sc.Tenant, kind, principal(r.Context()).UserID, req)
	if err != nil {
		return err
	}
	v := h.sessionView(r, &s)
	v.Key = s.Key // returned only here (FR-7.1)
	return writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) createKeyBased(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	return h.create(w, r, sc, session.KeyBased)
}

func (h *Handler) createURLBased(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	return h.create(w, r, sc, session.URLBased)
}

type sessionList struct {
	Sessions []sessionView `json:"sessions"`
	Meta     metaView      `json:"meta"`
}

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	kind := session.Kind(r.URL.Query().Get("kind"))
	if kind != "" && kind != session.KeyBased && kind != session.URLBased {
		return apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed, "kind must be key-based or url-based.")
	}
	all, err := h.filteredSessions(r, sc, kind)
	if err != nil {
		return err
	}
	shown := page(r, all)
	out := sessionList{Sessions: make([]sessionView, 0, len(shown)), Meta: metaView{Total: len(all)}}
	for i := range shown {
		out.Sessions = append(out.Sessions, h.sessionView(r, &shown[i]))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	s, err := h.svc.RequireSession(sc)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, h.sessionView(r, &s))
}

func (h *Handler) updateSession(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var p session.Patch
	if err := h.decode(r, &p); err != nil {
		return err
	}
	s, err := h.svc.UpdateSession(r.Context(), sc.Tenant, sc.SessionID, p)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, h.sessionView(r, &s))
}

func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if err := h.svc.DeleteSession(r.Context(), sc.Tenant, sc.SessionID); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) keepAlive(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	s, err := h.svc.KeepAlive(r.Context(), sc.Tenant, sc.SessionID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, h.sessionView(r, &s))
}

// filteredSessions lists the tenant's sessions narrowed by the query: state (active or expired), createdBy, createdByKey
// and name (FR-11.11a).
func (h *Handler) filteredSessions(r *http.Request, sc store.Scope, kind session.Kind) ([]session.Session, error) {
	q := r.URL.Query()
	state := q.Get("state")
	if state != "" && state != "active" && state != "expired" {
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed, "state must be active or expired.")
	}
	var all []session.Session
	if state == "" || state == "expired" {
		all = h.svc.Sessions.ListAll(sc.Tenant)
	} else {
		all = h.svc.Sessions.List(sc.Tenant, "")
	}
	now := h.svc.Clock.Now()
	return slices.DeleteFunc(all, func(s session.Session) bool {
		switch {
		case kind != "" && s.Kind != kind:
			return true
		case state == "expired" && !s.Expired(now), state == "active" && s.Expired(now):
			return true
		case q.Get("createdBy") != "" && s.CreatedBy != q.Get("createdBy"):
			return true
		case q.Get("createdByKey") != "" && s.CreatedByKey != q.Get("createdByKey"):
			return true
		case q.Get("name") != "" && !strings.Contains(strings.ToLower(s.Name), strings.ToLower(q.Get("name"))):
			return true
		}
		return false
	}), nil
}

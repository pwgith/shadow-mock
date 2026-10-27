package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
)

func matchSample(s *model.SampleRequest) *match.Request { return match.FromSample(s) }

func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var keep func(*journal.Entry) bool
	if since := r.URL.Query().Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed, "since must be an RFC 3339 date-time.")
		}
		keep = func(e *journal.Entry) bool { return !e.LoggedAt.Before(t) }
	}
	limit := 0
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	all := h.svc.Journal.Query(sc, keep, 0)
	shown := all
	if limit > 0 && limit < len(shown) {
		shown = shown[:limit]
	}
	out := serveEventList{
		Requests: make([]serveEvent, 0, len(shown)), Meta: metaView{Total: len(all)},
		RequestJournalDisabled: !h.svc.Journal.Enabled(),
	}
	for _, e := range shown {
		out.Requests = append(out.Requests, eventView(e))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (h *Handler) clearRequests(w http.ResponseWriter, _ *http.Request, sc store.Scope) error {
	h.svc.Journal.Remove(sc, nil)
	return ok(w)
}

func (h *Handler) getRequest(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	e, found := h.svc.Journal.Get(sc, r.PathValue("requestId"))
	if !found {
		return apierr.New(http.StatusNotFound, apierr.NotFound, "Request %s is not in the journal.", r.PathValue("requestId"))
	}
	return writeJSON(w, http.StatusOK, eventView(e))
}

func (h *Handler) deleteRequest(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	id := r.PathValue("requestId")
	if removed := h.svc.Journal.Remove(sc, func(e *journal.Entry) bool { return e.ID == id }); len(removed) == 0 {
		return apierr.New(http.StatusNotFound, apierr.NotFound, "Request %s is not in the journal.", id)
	}
	return ok(w)
}

func (h *Handler) pattern(r *http.Request) (*match.RequestMatcher, error) {
	var p model.RequestPattern
	if err := h.decode(r, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(""); err != nil {
		return nil, err
	}
	return h.svc.CompilePattern(&p)
}

type countResult struct {
	Count int `json:"count"`
}

func (h *Handler) countRequests(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	rm, err := h.pattern(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, countResult{Count: len(h.svc.FindRequests(sc, rm))})
}

func (h *Handler) findRequests(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	rm, err := h.pattern(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, findView(h.svc.FindRequests(sc, rm)))
}

func (h *Handler) removeRequests(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	rm, err := h.pattern(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, findView(h.svc.RemoveRequests(sc, rm)))
}

func (h *Handler) unmatchedRequests(w http.ResponseWriter, _ *http.Request, sc store.Scope) error {
	return writeJSON(w, http.StatusOK, findView(h.svc.UnmatchedRequests(sc)))
}

func (h *Handler) unmatchedNearMisses(w http.ResponseWriter, _ *http.Request, sc store.Scope) error {
	out := nearMissList{NearMisses: []nearMissView{}}
	for _, e := range h.svc.UnmatchedRequests(sc) {
		out.NearMisses = append(out.NearMisses, stubNearMisses(e.Request, h.svc.NearMissesFor(sc, e.Request), e)...)
	}
	return writeJSON(w, http.StatusOK, out)
}

func (h *Handler) nearMissesForRequest(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var sample model.SampleRequest
	if err := h.decode(r, &sample); err != nil {
		return err
	}
	if err := sample.Validate(); err != nil {
		return apierr.Invalid("", "%s", err.Error())
	}
	req := matchSample(&sample)
	return writeJSON(w, http.StatusOK, nearMissList{NearMisses: stubNearMisses(req, h.svc.NearMissesFor(sc, req), nil)})
}

func (h *Handler) nearMissesForPattern(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var p model.RequestPattern
	if err := h.decode(r, &p); err != nil {
		return err
	}
	rm, err := h.svc.CompilePattern(&p)
	if err != nil {
		return err
	}
	out := nearMissList{NearMisses: []nearMissView{}}
	for _, nm := range h.svc.NearMissesForPattern(sc, rm) {
		lr := requestView(nm.Entry.Request, nm.Entry)
		pat := p
		out.NearMisses = append(out.NearMisses, nearMissView{Request: &lr, RequestPattern: &pat, MatchResult: matchResult{Distance: round(nm.Distance)}})
	}
	return writeJSON(w, http.StatusOK, out)
}

func (h *Handler) exportRequests(_ http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	return apierr.New(http.StatusNotFound, apierr.NotSupported, "Journal export is a SaaS feature; use GET requests in standalone mode.")
}

func (h *Handler) listCounters(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	cs, err := h.svc.ListCounters(r.Context(), sc)
	if err != nil {
		return err
	}
	out := counterList{Counters: make([]counterView, 0, len(cs))}
	for _, c := range cs {
		out.Counters = append(out.Counters, toCounterView(c))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (h *Handler) resetCounters(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if err := h.svc.ResetCounters(r.Context(), sc); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) setCounter(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var body struct {
		Count     *int64 `json:"count"`
		StubLayer string `json:"stubLayer"`
	}
	if err := h.decode(r, &body); err != nil {
		return err
	}
	if body.Count == nil {
		return apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed, "count is required.")
	}
	c, err := h.svc.SetCounter(r.Context(), sc, r.PathValue("stubMappingId"), *body.Count, session.Kind(body.StubLayer))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toCounterView(c))
}

func (h *Handler) resetCounter(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if err := h.svc.ResetCounter(r.Context(), sc, r.PathValue("stubMappingId")); err != nil {
		return err
	}
	return ok(w)
}

// scenarios answers every /scenarios path: WireMock scenarios are not supported (FR-4.4).
func (h *Handler) scenarios(_ http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	return apierr.New(http.StatusNotFound, apierr.NotSupported,
		"WireMock scenarios are not supported. Use callResponses on the mapping to vary the response by call number.")
}

package admin

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
)

func matchSample(s *model.SampleRequest) *match.Request { return match.FromSample(s) }

// uuidPattern is the form WireMock demands of a request id or a stub id in a query.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func badRequest(format string, a ...any) error { return apierr.BadQuery(format, a...) }

// listRequests answers GET /requests. unmatched and matchingStub narrow the list, and meta.total
// counts what they leave, before since and limit cut it down, as in WireMock (FR-10.7).
func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	q := r.URL.Query()
	keep := func(*journal.Entry) bool { return true }
	if strings.EqualFold(q.Get("unmatched"), "true") {
		keep = func(e *journal.Entry) bool { return !e.Matched() }
	}
	if stub := q.Get("matchingStub"); stub != "" {
		if !uuidPattern.MatchString(stub) {
			return badRequest("Query parameter matchingStub value '%s' is not a valid UUID.", stub)
		}
		narrowed := keep
		keep = func(e *journal.Entry) bool { return narrowed(e) && e.Stub != nil && strings.EqualFold(e.Stub.ID, stub) }
	}
	keep, err := narrowJournal(q, keep)
	if err != nil {
		return err
	}
	since, limit, err := journalWindow(q)
	if err != nil {
		return err
	}
	all := h.svc.Journal.Query(sc, keep, 0)
	shown := all
	if !since.IsZero() {
		shown = slices.DeleteFunc(slices.Clone(shown), func(e *journal.Entry) bool { return e.LoggedAt.Before(since) })
	}
	shown, next, err := cutPage(q, shown, limit)
	if err != nil {
		return err
	}
	out := serveEventList{
		Requests: make([]serveEvent, 0, len(shown)), Meta: metaView{Total: len(all)},
		RequestJournalDisabled: !h.svc.Journal.Enabled(),
	}
	out.Meta.NextCursor = next
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

// deleteRequest removes one entry. An id that is not in the journal is still a 200, as in
// WireMock (FR-10.8); only an id that is not a UUID is refused.
func (h *Handler) deleteRequest(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	id := r.PathValue("requestId")
	if !uuidPattern.MatchString(id) {
		return badRequest("%s is not a valid UUID.", id)
	}
	h.svc.Journal.Remove(sc, func(e *journal.Entry) bool { return e.ID == id })
	return writeJSON(w, http.StatusOK, struct{}{})
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
	Count                  int  `json:"count"`
	RequestJournalDisabled bool `json:"requestJournalDisabled"`
}

func (h *Handler) countRequests(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	rm, err := h.pattern(r)
	if err != nil {
		return err
	}
	if !h.svc.Journal.Enabled() {
		// WireMock answers -1 when it keeps no journal, which is not the same as none matching.
		return writeJSON(w, http.StatusOK, countResult{Count: -1, RequestJournalDisabled: true})
	}
	return writeJSON(w, http.StatusOK, countResult{Count: len(h.svc.FindRequests(sc, rm))})
}

func (h *Handler) findRequests(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	rm, err := h.pattern(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, findView(h.svc.FindRequests(sc, rm), !h.svc.Journal.Enabled()))
}

func (h *Handler) removeRequests(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	rm, err := h.pattern(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, removedView(h.svc.RemoveRequests(sc, rm)))
}

// removeRequestsByMetadata removes the entries served by a stub whose metadata matches the body
// (FR-10.8).
func (h *Handler) removeRequestsByMetadata(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var p model.ValuePattern
	if err := h.decode(r, &p); err != nil {
		return err
	}
	removed, err := h.svc.RemoveRequestsByMetadata(sc, p)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, removedView(removed))
}

func (h *Handler) unmatchedRequests(w http.ResponseWriter, _ *http.Request, sc store.Scope) error {
	return writeJSON(w, http.StatusOK, findView(h.svc.UnmatchedRequests(sc), !h.svc.Journal.Enabled()))
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

// journalWindow reads since and limit; limit is -1 when none was given.
func journalWindow(q url.Values) (since time.Time, limit int, err error) {
	limit = -1
	if v := q.Get("since"); v != "" {
		if since, err = time.Parse(time.RFC3339, v); err != nil {
			return since, limit, badRequest("since must be an ISO 8601 date-time, not %q.", v)
		}
	}
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 0 {
			return since, -1, badRequest("limit must be 0 or more, not %q.", v)
		}
	}
	return since, limit, nil
}

// cutPage drops the entries before the cursor and keeps up to limit; next is the cursor for the page after.
func cutPage(q url.Values, shown []*journal.Entry, limit int) (page []*journal.Entry, next string, err error) {
	cursor := 0
	if v := q.Get("cursor"); v != "" {
		if cursor, err = strconv.Atoi(v); err != nil || cursor < 0 {
			return nil, "", badRequest("cursor must be a number from a previous answer, not %q.", v)
		}
		cursor = min(cursor, len(shown))
		shown = shown[cursor:]
	}
	if limit >= 0 && limit < len(shown) {
		return shown[:limit], strconv.Itoa(cursor + limit), nil
	}
	return shown, "", nil
}

// narrowJournal adds the managed-mode filters to keep: status, method, path (a prefix), session, layer, matched, and
// from and to (FR-11.11b).
func narrowJournal(q url.Values, keep func(*journal.Entry) bool) (func(*journal.Entry) bool, error) {
	var tests []func(*journal.Entry) bool
	for _, build := range []func(url.Values) ([]func(*journal.Entry) bool, error){fieldFilters, stateFilters, rangeFilters} {
		more, err := build(q)
		if err != nil {
			return nil, err
		}
		tests = append(tests, more...)
	}
	return func(e *journal.Entry) bool {
		return keep(e) && !slices.ContainsFunc(tests, func(t func(*journal.Entry) bool) bool { return !t(e) })
	}, nil
}

// fieldFilters match what the request and response said: status, method, path and layer.
func fieldFilters(q url.Values) ([]func(*journal.Entry) bool, error) {
	var tests []func(*journal.Entry) bool
	if v := q.Get("status"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, badRequest("status must be a number, not %q.", v)
		}
		tests = append(tests, func(e *journal.Entry) bool { return e.Response.Status == n })
	}
	if v := q.Get("method"); v != "" {
		tests = append(tests, func(e *journal.Entry) bool { return e.Request != nil && strings.EqualFold(e.Request.Method, v) })
	}
	if v := q.Get("path"); v != "" {
		tests = append(tests, func(e *journal.Entry) bool { return e.Request != nil && strings.HasPrefix(e.Request.Path, v) })
	}
	if v := q.Get("layer"); v != "" {
		tests = append(tests, func(e *journal.Entry) bool { return layerOf(e) == v })
	}
	return tests, nil
}

// stateFilters match what the engine did with the request: which shadow mock it was for, and whether a mapping matched.
func stateFilters(q url.Values) ([]func(*journal.Entry) bool, error) {
	var tests []func(*journal.Entry) bool
	if v := q.Get("session"); v != "" {
		tests = append(tests, func(e *journal.Entry) bool { return e.SessionID == v || slices.Contains(e.Sessions, v) })
	}
	if v := q.Get("matched"); v != "" {
		want, err := strconv.ParseBool(v)
		if err != nil {
			return nil, badRequest("matched must be true or false, not %q.", v)
		}
		tests = append(tests, func(e *journal.Entry) bool { return e.Matched() == want })
	}
	return tests, nil
}

// rangeFilters keep the entries logged from the "from" time up to the "to" time.
func rangeFilters(q url.Values) ([]func(*journal.Entry) bool, error) {
	var tests []func(*journal.Entry) bool
	for _, name := range []string{"from", "to"} {
		v := q.Get(name)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, badRequest("%s must be an ISO 8601 date-time, not %q.", name, v)
		}
		if name == "to" {
			tests = append(tests, func(e *journal.Entry) bool { return e.LoggedAt.Before(t) })
		} else {
			tests = append(tests, func(e *journal.Entry) bool { return !e.LoggedAt.Before(t) })
		}
	}
	return tests, nil
}

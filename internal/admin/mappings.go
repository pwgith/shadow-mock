package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
)

// page applies limit and offset query parameters.
func page[T any](r *http.Request, xs []T) []T {
	q := r.URL.Query()
	if off, err := strconv.Atoi(q.Get("offset")); err == nil && off > 0 {
		if off >= len(xs) {
			return xs[:0]
		}
		xs = xs[off:]
	}
	if lim, err := strconv.Atoi(q.Get("limit")); err == nil && lim > 0 && lim < len(xs) {
		xs = xs[:lim]
	}
	return xs
}

func (h *Handler) listMappings(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	ms, err := h.svc.ListMappings(r.Context(), sc)
	if err != nil {
		return err
	}
	total := len(ms)
	ms = page(r, ms)
	if ms == nil {
		ms = []model.StubMapping{}
	}
	return writeJSON(w, http.StatusOK, mappingList{Mappings: ms, Meta: metaView{Total: total}})
}

func (h *Handler) decodeMapping(r *http.Request) (model.StubMapping, error) {
	b, err := h.readBody(r)
	if err != nil {
		return model.StubMapping{}, err
	}
	return h.svc.DecodeAndValidate(b)
}

func (h *Handler) createMapping(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	m, err := h.decodeMapping(r)
	if err != nil {
		return err
	}
	if m, err = h.svc.PutMapping(r.Context(), sc, m); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, m)
}

func (h *Handler) getMapping(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	m, err := h.svc.GetMapping(r.Context(), sc, r.PathValue("stubMappingId"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, m)
}

func (h *Handler) replaceMapping(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	m, err := h.decodeMapping(r)
	if err != nil {
		return err
	}
	if m, err = h.svc.ReplaceMapping(r.Context(), sc, r.PathValue("stubMappingId"), m); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, m)
}

func (h *Handler) deleteMapping(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if err := h.svc.DeleteMapping(r.Context(), sc, r.PathValue("stubMappingId")); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) deleteAllMappings(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if err := h.svc.DeleteAllMappings(r.Context(), sc); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) importMappings(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var body struct {
		Mappings      []json.RawMessage  `json:"mappings"`
		ImportOptions mock.ImportOptions `json:"importOptions"`
	}
	if err := h.decode(r, &body); err != nil {
		return err
	}
	ms := make([]model.StubMapping, 0, len(body.Mappings))
	for i, raw := range body.Mappings {
		m, err := model.DecodeStubMapping(raw)
		if err != nil {
			return pointerPrefix(err, "/mappings/"+strconv.Itoa(i))
		}
		ms = append(ms, m)
	}
	if err := h.svc.Import(r.Context(), sc, ms, body.ImportOptions); err != nil {
		return err
	}
	return ok(w)
}

func pointerPrefix(err error, prefix string) error {
	var ve *apierr.ValidationError
	if errors.As(err, &ve) {
		for i := range ve.Errors {
			if ve.Errors[i].Source != nil {
				ve.Errors[i].Source.Pointer = prefix + ve.Errors[i].Source.Pointer
			}
		}
	}
	return err
}

func (h *Handler) resetMappings(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if err := h.svc.ResetMappings(r.Context(), sc); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) saveMappings(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if err := h.svc.SaveMappings(r.Context(), sc); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) unmatchedMappings(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	ms, err := h.svc.UnmatchedMappings(r.Context(), sc)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, mappingList{Mappings: ms, Meta: metaView{Total: len(ms)}})
}

func (h *Handler) findByMetadata(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var p model.ValuePattern
	if err := h.decode(r, &p); err != nil {
		return err
	}
	ms, err := h.svc.FindByMetadata(r.Context(), sc, p)
	if err != nil {
		return err
	}
	if ms == nil {
		ms = []model.StubMapping{}
	}
	return writeJSON(w, http.StatusOK, mappingList{Mappings: ms, Meta: metaView{Total: len(ms)}})
}

func (h *Handler) removeByMetadata(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var p model.ValuePattern
	if err := h.decode(r, &p); err != nil {
		return err
	}
	if err := h.svc.RemoveByMetadata(r.Context(), sc, p); err != nil {
		return err
	}
	return ok(w)
}

type validateResult struct {
	Valid  bool                   `json:"valid"`
	Errors []apierr.WireMockError `json:"errors,omitempty"`
}

// validateMapping reports what is wrong with a candidate mapping. It always returns 200.
func (h *Handler) validateMapping(w http.ResponseWriter, r *http.Request, _ store.Scope) error {
	b, err := h.readBody(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.DecodeAndValidate(b); err != nil {
		return writeJSON(w, http.StatusOK, validateResult{Errors: asWireMockErrors(err)})
	}
	return writeJSON(w, http.StatusOK, validateResult{Valid: true})
}

func asWireMockErrors(err error) []apierr.WireMockError {
	var ve *apierr.ValidationError
	if errors.As(err, &ve) {
		return ve.Errors
	}
	var ae *apierr.Error
	if errors.As(err, &ae) {
		return []apierr.WireMockError{{Code: 10, Title: ae.Message, Detail: string(ae.Code)}}
	}
	return []apierr.WireMockError{{Code: 10, Title: err.Error()}}
}

type dryRunResponse struct {
	Status  int            `json:"status"`
	Headers map[string]any `json:"headers,omitempty"`
	Body    string         `json:"body,omitempty"`
}

type dryRunView struct {
	Matched     bool               `json:"matched"`
	Layer       string             `json:"layer,omitempty"`
	SessionID   *string            `json:"sessionId"`
	StubMapping *model.StubMapping `json:"stubMapping,omitempty"`
	Response    *dryRunResponse    `json:"response,omitempty"`
	NearMisses  []nearMissView     `json:"nearMisses,omitempty"`
}

func (h *Handler) dryRun(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var req mock.DryRunRequest
	if err := h.decode(r, &req); err != nil {
		return err
	}
	if err := req.Request.Validate(); err != nil {
		return apierr.Invalid("/request", "%s", err.Error())
	}
	res, err := h.svc.DryRun(r.Context(), sc, req)
	if err != nil {
		return err
	}
	v := dryRunView{Matched: res.Matched, StubMapping: res.Stub}
	if res.Matched {
		v.Layer = string(res.Layer)
		v.Response = &dryRunResponse{Status: res.Status, Headers: headerMap(res.Header), Body: bodyText(res.Body)}
	} else {
		v.NearMisses = stubNearMisses(matchSample(&req.Request), res.NearMisses, nil)
	}
	if res.SessionID != "" {
		v.SessionID = &res.SessionID
	}
	return writeJSON(w, http.StatusOK, v)
}

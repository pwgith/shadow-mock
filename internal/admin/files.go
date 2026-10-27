package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/store"
)

func fileErr(err error, name string) error {
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(http.StatusNotFound, apierr.NotFound, "File %s was not found.", name)
	}
	return err
}

func (h *Handler) listFiles(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	names, err := h.svc.Files.List(r.Context(), sc)
	if err != nil {
		return err
	}
	if names == nil {
		names = []string{}
	}
	return writeJSON(w, http.StatusOK, names)
}

func (h *Handler) getFile(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	name := r.PathValue("fileId")
	b, err := h.svc.Files.Read(r.Context(), sc, name)
	if err != nil {
		return fileErr(err, name)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b) //nolint:gosec // G705: an octet-stream download, not HTML; a failed write means the client left
	return nil
}

func (h *Handler) putFile(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	b, err := h.readBody(r)
	if err != nil {
		return err
	}
	if err := h.svc.Files.Put(r.Context(), sc, r.PathValue("fileId"), b); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	name := r.PathValue("fileId")
	if err := h.svc.Files.Delete(r.Context(), sc, name); err != nil {
		return fileErr(err, name)
	}
	return ok(w)
}

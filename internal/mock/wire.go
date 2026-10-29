package mock

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
)

// filesPrefix is where the files in __files are served (FR-10.11).
const filesPrefix = "/__files/"

// markMatched adds the headers WireMock puts on every reply a mapping produced: the id of the
// mapping, and its name when it has one (FR-10.10).
func markMatched(h http.Header, stub *model.StubMapping) {
	if stub == nil {
		return
	}
	h.Set("Matched-Stub-Id", stub.ID)
	if stub.Name != "" {
		h.Set("Matched-Stub-Name", stub.Name)
	}
}

// gunzipBody inflates a request body sent with Content-Encoding: gzip, so it is matched,
// templated and journalled as text. A body that is not valid gzip is left as it was. The result
// is capped at limit bytes (0 means no cap) so a small body cannot expand without bound.
func gunzipBody(h http.Header, body []byte, limit int64) []byte {
	if len(body) == 0 || !strings.EqualFold(strings.TrimSpace(h.Get("Content-Encoding")), "gzip") {
		return body
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body
	}
	defer zr.Close()
	var r io.Reader = zr
	if limit > 0 {
		r = io.LimitReader(zr, limit+1)
	}
	out, err := io.ReadAll(r)
	if err != nil || (limit > 0 && int64(len(out)) > limit) {
		return body
	}
	return out
}

// stubCORS adds the headers WireMock's CORS filter adds, and answers a preflight request itself.
// It reports whether it did, in which case nothing more is to be done. A header a mapping sets
// for itself replaces these when the reply is written.
func stubCORS(w http.ResponseWriter, r *http.Request) (answered bool) {
	h := w.Header()
	h.Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Credentials", "true")
	if r.Method != http.MethodOptions || r.Header.Get("Access-Control-Request-Method") == "" {
		return false
	}
	h.Set("Access-Control-Max-Age", "1800")
	h.Set("Access-Control-Allow-Methods", "OPTIONS,GET,POST,PUT,PATCH,DELETE")
	h.Set("Access-Control-Allow-Headers", joinHeaderList(r.Header.Get("Access-Control-Request-Headers")))
	h.Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
	return true
}

// joinHeaderList writes a comma-separated header list without the spaces, as WireMock does.
func joinHeaderList(list string) string {
	parts := strings.Split(list, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ",")
}

// serveFile answers with a file from __files, and reports whether it did. A name that climbs out
// of the folder, or a file that is not there, is not served.
func (s *Service) serveFile(ctx context.Context, w http.ResponseWriter, r *http.Request, tr Traffic, name string) bool {
	name, err := url.PathUnescape(strings.TrimPrefix(name, "/"))
	name = strings.TrimSuffix(name, "/")
	if err != nil || name == "" || slices.Contains(strings.Split(name, "/"), "..") {
		return false
	}
	sc := store.Scope{Tenant: tr.Tenant}
	body, err := s.Files.Read(ctx, sc, name)
	if err != nil {
		if s.isFolder(ctx, sc, name) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		return false
	}
	h := w.Header()
	if t, _, _ := strings.Cut(mime.TypeByExtension(path.Ext(name)), ";"); t != "" {
		h.Set("Content-Type", t)
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	return true
}

// isFolder reports whether name is a folder in __files, which WireMock refuses with a 403.
func (s *Service) isFolder(ctx context.Context, sc store.Scope, name string) bool {
	names, err := s.Files.List(ctx, sc)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, name+"/") })
}

// notFoundFile answers a /__files/ request for a file that is not there.
func notFoundFile(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, "No such file in __files.\n") //nolint:errcheck // the client went away
}

// proxyFailure answers a request whose upstream could not be reached, as WireMock does: a 500
// whose body says so (FR-10.11). target is where the request was going.
func (s *Service) proxyFailure(w http.ResponseWriter, sv *served, target string, cause error) {
	root := cause
	for next := errors.Unwrap(root); next != nil; next = errors.Unwrap(root) {
		root = next
	}
	body := []byte("Network failure trying to make a proxied request from Shadow Mock to " + target + "\r\n" + root.Error())
	sv.entry.Response = journal.Response{Status: http.StatusInternalServerError, Header: http.Header{}, Body: body}
	s.finish(sv) // recorded before the client can see the response (FR-5.5)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write(body) //nolint:gosec // the error text names the target and the cause, and is sent as plain text
}

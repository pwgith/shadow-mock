package admin

import (
	"net/http"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/record"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
)

type settingsEnvelope struct {
	Settings respond.Settings `json:"settings"`
}

func (h *Handler) getSettings(w http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	return writeJSON(w, http.StatusOK, settingsEnvelope{Settings: h.svc.Renderer.Settings()})
}

// updateSettings replaces the settings, as POST and PUT do in WireMock. proxyPassThrough keeps its
// value when the body does not name it.
func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request, _ store.Scope) error {
	var body struct {
		respond.Settings
		ProxyPassThrough *bool `json:"proxyPassThrough"`
	}
	if err := h.decode(r, &body); err != nil {
		return err
	}
	s := body.Settings
	s.ProxyPassThrough = h.svc.Renderer.Settings().ProxyPassThrough
	if body.ProxyPassThrough != nil {
		s.ProxyPassThrough = *body.ProxyPassThrough
	}
	if s.FixedDelay < 0 {
		return apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed, "fixedDelay must not be negative.")
	}
	h.svc.Renderer.SetSettings(s)
	return ok(w)
}

func (h *Handler) resetAll(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	if err := h.svc.ResetAll(r.Context(), sc); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) shutdown(w http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	if h.cfg.SaaS || h.cfg.Shutdown == nil {
		return apierr.New(http.StatusNotFound, apierr.NotAvailableInSaaS, "Shutdown is not available here.")
	}
	if err := ok(w); err != nil {
		return err
	}
	if f, isFlusher := w.(http.Flusher); isFlusher {
		f.Flush()
	}
	go h.cfg.Shutdown()
	return nil
}

type versionView struct {
	Version string `json:"version"`
}

func (h *Handler) version(w http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	return writeJSON(w, http.StatusOK, versionView{Version: h.cfg.Version})
}

// Health is the Health schema.
type Health struct {
	Status          string            `json:"status"`
	Message         string            `json:"message,omitempty"`
	Version         string            `json:"version"`
	UptimeInSeconds int64             `json:"uptimeInSeconds"`
	Timestamp       time.Time         `json:"timestamp"`
	Checks          map[string]string `json:"checks,omitempty"`
}

// Health reports the engine as healthy, for /__health, /__ready and /__admin/health.
func (h *Handler) Health(checks map[string]string) Health {
	now := h.svc.Clock.Now()
	return Health{
		Status: "healthy", Version: h.cfg.Version, UptimeInSeconds: int64(now.Sub(h.startedAt).Seconds()),
		Timestamp: now.UTC(), Checks: checks,
	}
}

func (h *Handler) liveStats(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "15m"
	}
	d, err := time.ParseDuration(window)
	if err != nil || (window != "5m" && window != "15m" && window != "1h") {
		return apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed, "window must be 5m, 15m or 1h.")
	}
	active := stats.ActiveSessions{
		KeyBased: len(h.svc.Sessions.List(sc.Tenant, session.KeyBased)),
		URLBased: len(h.svc.Sessions.List(sc.Tenant, session.URLBased)),
	}
	return writeJSON(w, http.StatusOK, h.svc.Stats.Live(sc.Tenant, window, d, h.svc.Clock.Now(), active))
}

func (h *Handler) usage(_ http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	return apierr.New(http.StatusNotFound, apierr.NotSupported, "Standalone and central-server mode have no plans or usage.")
}

// recordings tracks recording state per scope. Capturing traffic needs proxying, which
// arrives in P3, so a recording currently produces no mappings.

type recordingStatus struct {
	Status string `json:"status"`
}

type snapshotResult struct {
	Mappings []model.StubMapping `json:"mappings"`
}

func (h *Handler) startRecording(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var spec record.Spec
	if err := h.decode(r, &spec); err != nil {
		return err
	}
	if err := h.svc.StartRecording(sc, spec); err != nil {
		return err
	}
	return ok(w)
}

func (h *Handler) stopRecording(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	ms, err := h.svc.StopRecording(r.Context(), sc)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, snapshotResult{Mappings: ms})
}

func (h *Handler) recordingStatus(w http.ResponseWriter, _ *http.Request, sc store.Scope) error {
	return writeJSON(w, http.StatusOK, recordingStatus{Status: h.svc.RecordingStatus(sc)})
}

func (h *Handler) snapshot(w http.ResponseWriter, r *http.Request, sc store.Scope) error {
	var spec record.Spec
	if err := h.decode(r, &spec); err != nil {
		return err
	}
	ms, err := h.svc.Snapshot(r.Context(), sc, spec)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, snapshotResult{Mappings: ms})
}

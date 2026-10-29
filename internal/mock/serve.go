package mock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/detect"
	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/ids"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Traffic is a mock request after routing (DESIGN.md §8).
type Traffic struct {
	Tenant tenant.ID
	// URLMockID is the URL-based shadow mock from a /d/<id> prefix, or "".
	URLMockID string
	// Path is the escaped path to match, with the org and /d/<id> prefixes stripped.
	Path        string
	ClientIP    string
	AbsoluteURL string
}

// served is what one mock request produced, for the journal and stats.
type served struct {
	done  bool // the journal entry and stats are recorded once
	entry *journal.Entry
	start time.Time
	stage string
	ctx   context.Context //nolint:containedctx // the request's context, kept to log and trace when it finishes
	span  trace.Span
}

// ServeMock handles one mock request end to end.
func (s *Service) ServeMock(w http.ResponseWriter, r *http.Request, tr Traffic) {
	ctx, span := s.Telemetry.Start(r, "mock "+r.Method)
	r = r.WithContext(ctx)
	sv := &served{start: s.Clock.Now(), ctx: ctx, span: span, entry: &journal.Entry{
		ID: ids.NewUUID(), LoggedAt: s.Clock.Now().UTC(), Tenant: tr.Tenant, AbsoluteURL: tr.AbsoluteURL,
	}}
	defer s.finish(sv)
	done := s.trackInFlight(tr.Path)
	defer done()

	if s.Config.StubCORS && stubCORS(w, r) {
		sv.entry.Request = nil // a preflight request is not journalled
		return
	}
	if s.Config.ServeFiles && tr.URLMockID == "" && strings.HasPrefix(tr.Path, filesPrefix) {
		sv.entry.Request = nil // files are not mock traffic, so they are not journalled
		if !s.serveFile(ctx, w, r, tr, strings.TrimPrefix(tr.Path, filesPrefix)) {
			notFoundFile(w)
		}
		return
	}
	body, err := readBody(w, r, s.Config.MaxBodyBytes)
	if err != nil {
		s.fail(w, sv, match.NewRequest(r, nil, tr.Path), err)
		return
	}
	body = gunzipBody(r.Header, body, s.Config.MaxBodyBytes)
	req := match.NewRequest(r, body, tr.Path)
	if tr.ClientIP != "" {
		req.ClientIP = tr.ClientIP
	}
	sv.entry.Request = req

	stack, err := s.trafficStack(ctx, tr, req, sv)
	if err != nil {
		s.fail(w, sv, req, err)
		return
	}
	res := engine.Resolve(req, stack)
	if res.Stub == nil {
		if s.Config.ServeFiles && tr.URLMockID == "" && s.serveFile(ctx, w, r, tr, tr.Path) {
			sv.entry.Request = nil
			return
		}
		if rec := s.recordingFor(stack); rec != nil {
			s.forward(ctx, w, r, req, &model.ResponseDefinition{ProxyBaseURL: rec.Spec.TargetBaseURL}, nil, rec, sv)
			return
		}
		s.unmatched(w, req, stack, sv)
		return
	}
	s.respond(ctx, w, r, req, stack, res, sv)
}

func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	if limit > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	b, err := io.ReadAll(r.Body)
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return nil, apierr.New(http.StatusRequestEntityTooLarge, apierr.BodyTooLarge,
			"The request body exceeds %d bytes.", limit)
	}
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return b, nil
}

// trafficStack checks the URL-based shadow mock, runs detection and builds the stack.
func (s *Service) trafficStack(_ context.Context, tr Traffic, req *match.Request, sv *served) (engine.Stack, error) {
	var sessions []string
	if tr.URLMockID != "" {
		sess, ok := s.Sessions.Get(tr.Tenant, tr.URLMockID)
		if !ok || sess.Kind != session.URLBased {
			// Never fall back to global silently (FR-8.6).
			return nil, apierr.New(http.StatusNotFound, apierr.UnknownURLBasedMock,
				"No active URL-based shadow mock %s.", tr.URLMockID)
		}
	}
	hit, found, err := s.Detector.Detect(tr.Tenant, tr.URLMockID, req)
	if hit.Stage != "" || hit.ScanTruncated {
		sv.entry.Detection = &journal.Detection{
			Stage: string(hit.Stage), Offset: hit.Offset,
			ScanTruncated: hit.ScanTruncated, Ambiguous: hit.Ambiguous,
		}
	}
	if err != nil {
		return nil, apierr.New(detect.StatusFor(err), apierr.AmbiguousSession,
			"The request matches several key-based shadow mocks: %s.", strings.Join(hit.Ambiguous, ", "))
	}
	// The index can lag a session that has just expired, so confirm the session is still active.
	if _, active := s.Sessions.Get(tr.Tenant, hit.SessionID); found && active {
		sessions = append(sessions, hit.SessionID)
		sv.stage = string(hit.Stage)
	}
	if tr.URLMockID != "" {
		sessions = append(sessions, tr.URLMockID)
	}
	sv.entry.Sessions = sessions
	if len(sessions) > 0 {
		sv.entry.SessionID = sessions[0]
	}
	return s.Layers.Stack(tr.Tenant, sessions...), nil
}

func (s *Service) respond(ctx context.Context, w http.ResponseWriter, r *http.Request, req *match.Request, stack engine.Stack, res engine.Result, sv *served) {
	stub := &res.Stub.Mapping
	def := &stub.Response
	sv.entry.Stub, sv.entry.StubScope, sv.entry.Layer = stub, res.Stub.Scope, string(res.Layer.Kind)
	var callNumber int64
	if stub.HasCallResponses() || stub.UsesCallNumber {
		n, err := s.Counters.Next(ctx, counter.Key{RequestScope: stack.RequestScope(), StubScope: res.Stub.Scope, StubID: stub.ID}, s.Clock.Now().UTC())
		if err != nil {
			s.fail(w, sv, req, fmt.Errorf("call counter: %w", err))
			return
		}
		sv.entry.CallNumber, callNumber = n, n
		if stub.HasCallResponses() {
			def = stub.ResponseFor(n)
		}
	}
	sv.entry.Definition = def
	in := &respond.Input{ID: sv.entry.ID, Request: req, PathParams: res.Stub.Matcher.PathParams(req), CallNumber: callNumber}
	if def.ProxyBaseURL != "" {
		s.forward(ctx, w, r, req, def, in, nil, sv)
		return
	}
	out, err := s.Renderer.Render(ctx, def, fileScopes(stack), in)
	if err != nil {
		s.fail(w, sv, req, err)
		return
	}
	markMatched(out.Header, stub)
	if s.Config.MatchedHeader {
		out.Header.Set("X-Shadow-Mock-Matched", layerLabel(res.Layer.Scope)+";layer="+string(res.Layer.Kind))
	}
	sv.entry.Response = journal.Response{Status: out.Status, Header: out.Header.Clone(), Body: out.Body}
	sv.entry.AddedDelay = out.Delay.Milliseconds()
	if err := s.Renderer.Write(ctx, w, r, out, func() { s.finish(sv) }); err != nil {
		s.Logger.DebugContext(ctx, "response not completed", "error", err.Error())
	}
	if s.Webhooks != nil {
		s.Webhooks.Fire(stub.Webhooks, in, def.TransformerParameters, out)
	}
}

// unmatched sends WireMock's 404 diagnostic with the closest stubs (FR-3.6).
func (s *Service) unmatched(w http.ResponseWriter, req *match.Request, stack engine.Stack, sv *served) {
	near := engine.NearMisses(req, stack, 3)
	var b strings.Builder
	b.WriteString("Request was not matched\n=======================\n\n")
	fmt.Fprintf(&b, "Request: %s %s\n", req.Method, req.URL())
	if len(near) == 0 {
		b.WriteString("\nNo mappings are defined.\n")
	} else {
		b.WriteString("\nClosest stubs:\n")
		for _, nm := range near {
			name := nm.Stub.Mapping.Name
			if name == "" {
				name = nm.Stub.ID()
			}
			fmt.Fprintf(&b, "  %s (%s layer, distance %.2f)\n", name, nm.Layer.Kind, nm.Distance)
			for _, why := range nm.Stub.Matcher.Mismatches(req) {
				fmt.Fprintf(&b, "    %s does not match\n", why)
			}
		}
	}
	body := []byte(b.String())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	sv.entry.Response = journal.Response{Status: http.StatusNotFound, Header: w.Header().Clone(), Body: body}
	s.finish(sv) // recorded before the client can see the response (FR-5.5)
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(body)
}

// fail sends an engine error on a mock path and records it.
func (s *Service) fail(w http.ResponseWriter, sv *served, req *match.Request, err error) {
	if sv.entry.Request == nil {
		sv.entry.Request = req
	}
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		s.Logger.Error("mock request failed", "error", err.Error())
		ae = apierr.New(http.StatusInternalServerError, "internal_error", "The engine could not serve this request.")
	}
	sv.entry.Response = journal.Response{
		Status: ae.Status, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(ae.Message),
	}
	s.finish(sv) // recorded before the client can see the response (FR-5.5)
	ae.Write(w)
}

func (s *Service) finish(sv *served) {
	if sv.done {
		return
	}
	sv.done = true
	e := sv.entry
	e.ProcessTime = s.Clock.Now().Sub(sv.start).Milliseconds()
	if e.Request != nil {
		s.Journal.Append(e)
	}
	path := ""
	if e.Request != nil {
		path = e.Request.Path
	}
	if s.Metrics != nil && e.Request != nil {
		s.Metrics.Request(string(e.Tenant), e.Request.Method, e.Response.Status, e.Layer, sv.stage)
	}
	s.Stats.Record(stats.Event{
		At: s.Clock.Now(), Tenant: e.Tenant, Layer: e.Layer, Path: path,
		Latency: s.Clock.Now().Sub(sv.start), Stage: sv.stage,
	})
	s.observe(sv, path)
}

// Render exposes rendering for dry runs.
func (s *Service) render(ctx context.Context, res engine.Result, stack engine.Stack, req *match.Request, n int64) (*respond.Response, error) {
	def := &res.Stub.Mapping.Response
	if res.Stub.Mapping.HasCallResponses() {
		def = res.Stub.Mapping.ResponseFor(n)
	}
	in := &respond.Input{Request: req, PathParams: res.Stub.Matcher.PathParams(req), CallNumber: n}
	return s.Renderer.Render(ctx, def, fileScopes(stack), in)
}

// layerLabel names a layer in the X-Shadow-Mock-Matched header: its session id, or "global".
func layerLabel(sc store.Scope) string {
	if sc.IsGlobal() {
		return "global"
	}
	return sc.SessionID
}

// trackInFlight counts a request as in flight on its path until the returned function runs.
func (s *Service) trackInFlight(path string) func() {
	c, _ := s.inflight.LoadOrStore(path, new(atomic.Int64))
	n := c.(*atomic.Int64) //nolint:errcheck,forcetypeassert // only *atomic.Int64 is ever stored
	n.Add(1)
	return func() { n.Add(-1) }
}

// InFlight reports how many requests for a path are being served right now. A request is in
// flight from the moment it arrives until its response is complete or its client has gone.
func (s *Service) InFlight(path string) int {
	if c, ok := s.inflight.Load(path); ok {
		return int(c.(*atomic.Int64).Load()) //nolint:errcheck,forcetypeassert // only *atomic.Int64 is ever stored
	}
	return 0
}

// observe records the finished request in its trace and, when log.requests is on, in the log. The
// path is logged and traced, never the query, because a query can carry a session key or a token.
func (s *Service) observe(sv *served, path string) {
	e := sv.entry
	method := ""
	if e.Request != nil {
		method = e.Request.Method
	}
	if sv.span != nil {
		attrs := []attribute.KeyValue{
			attribute.String("http.request.method", method),
			attribute.String("url.path", path),
			attribute.Int("http.response.status_code", e.Response.Status),
			attribute.String("shadowmock.layer", e.Layer),
			attribute.String("shadowmock.detection_stage", sv.stage),
			attribute.Bool("shadowmock.matched", e.Matched()),
		}
		if e.Stub != nil {
			attrs = append(attrs, attribute.String("shadowmock.stub_id", e.Stub.ID))
		}
		if e.SessionID != "" {
			attrs = append(attrs, attribute.String("shadowmock.session_id", e.SessionID))
		}
		sv.span.SetAttributes(attrs...)
		if e.Response.Status >= http.StatusInternalServerError {
			sv.span.SetStatus(codes.Error, http.StatusText(e.Response.Status))
		}
		sv.span.End()
	}
	if s.Config.LogRequests && s.Logger != nil {
		s.Logger.InfoContext(sv.ctx, "request served",
			"method", method, "path", path, "status", e.Response.Status, "layer", e.Layer,
			"stage", sv.stage, "duration_ms", s.Clock.Now().Sub(sv.start).Milliseconds(), "session_id", e.SessionID)
	}
}

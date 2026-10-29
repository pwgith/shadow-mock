package mock

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/proxy"
	"github.com/pwgith/shadow-mock/internal/record"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/store"
)

// proxyOptions are the proxy settings of a response definition.
func proxyOptions(def *model.ResponseDefinition) proxy.Options {
	return proxy.Options{
		AdditionalHeaders: def.AdditionalProxyRequestHeaders,
		RemoveHeaders:     def.RemoveProxyRequestHeaders,
		PrefixToRemove:    def.ProxyURLPrefixToRemove,
	}
}

// recordingFor returns the running recording that sees a request: the most specific scope
// in the stack that has one.
func (s *Service) recordingFor(stack engine.Stack) *record.Active {
	if s.Recorder == nil {
		return nil
	}
	scopes := make([]store.Scope, len(stack))
	for i, l := range stack {
		scopes[i] = l.Scope
	}
	return s.Recorder.Find(scopes)
}

// forward sends the request upstream and relays the answer. When rec is set, the exchange is
// also recorded if it passes the recording's filter.
func (s *Service) forward(ctx context.Context, w http.ResponseWriter, r *http.Request, req *match.Request, def *model.ResponseDefinition, in *respond.Input, rec *record.Active, sv *served) {
	sv.entry.Definition = def
	if s.Proxy == nil {
		s.fail(w, sv, req, apierr.New(http.StatusNotImplemented, apierr.NotSupported, "Proxying is not available."))
		return
	}
	target, err := s.Renderer.RenderText(def, in, def.ProxyBaseURL)
	if err != nil {
		s.fail(w, sv, req, err)
		return
	}
	res, err := s.Proxy.Forward(ctx, req, target, proxyOptions(def))
	if err != nil {
		s.Logger.WarnContext(ctx, "proxy failed", "target", target, "error", err.Error())
		code := proxy.CodeBadGateway
		var pe *proxy.Error
		if errors.As(err, &pe) {
			code = pe.Code
		}
		switch code {
		case proxy.CodeBadGateway:
			s.proxyFailure(w, sv, target, err)
			return
		case proxy.CodeDenied:
			s.proxyDenied(w, sv)
			return
		}
		s.fail(w, sv, req, apierr.New(http.StatusBadGateway, apierr.Code(code), "The upstream server could not be reached."))
		return
	}
	if rec != nil && rec.Accepts(req) {
		if err := rec.Add(record.Exchange{Request: req, Status: res.Status, Header: res.Header.Clone(), Body: res.Body}); err != nil {
			s.Logger.WarnContext(ctx, "could not write a recorded mapping", "error", err.Error())
		}
	}
	out := s.Renderer.FromUpstream(def, res.Status, res.Header, res.Body)
	markMatched(out.Header, sv.entry.Stub)
	sv.entry.Response = journal.Response{Status: out.Status, Header: out.Header.Clone(), Body: out.Body}
	sv.entry.AddedDelay = out.Delay.Milliseconds()
	if err := s.Renderer.Write(ctx, w, r, out, func() { s.finish(sv) }); err != nil {
		s.Logger.DebugContext(ctx, "response not completed", "error", err.Error())
	}
}

// StartRecording begins recording unmatched traffic in a scope.
func (s *Service) StartRecording(sc store.Scope, spec record.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	var filter *match.RequestMatcher
	if spec.Filters != nil {
		var err error
		if filter, err = s.CompilePattern(spec.Filters); err != nil {
			return err
		}
	}
	return s.Recorder.Start(sc, spec, filter)
}

// StartLiveRecording is StartRecording for a recording that writes each new request's mapping to the
// scope's store, as a persistent mapping, as soon as the request has been proxied.
func (s *Service) StartLiveRecording(ctx context.Context, sc store.Scope, spec record.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	var filter *match.RequestMatcher
	if spec.Filters != nil {
		var err error
		if filter, err = s.CompilePattern(spec.Filters); err != nil {
			return err
		}
	}
	return s.Recorder.StartLive(sc, spec, filter, func(m model.StubMapping, files []record.File) error {
		detached := context.WithoutCancel(ctx)
		if err := s.putFiles(detached, sc, files); err != nil {
			return err
		}
		_, err := s.PutMapping(detached, sc, m.WithPersistent())
		return err
	})
}

// RecordingStatus reports the state of a scope's recording.
func (s *Service) RecordingStatus(sc store.Scope) string { return s.Recorder.Status(sc) }

// StopRecording ends a scope's recording and returns the mappings it produced, storing them
// unless the spec asked not to. Stopping when nothing is recording returns no mappings.
func (s *Service) StopRecording(ctx context.Context, sc store.Scope) ([]model.StubMapping, error) {
	a := s.Recorder.Stop(sc)
	if a == nil {
		return []model.StubMapping{}, nil
	}
	ms, files, err := record.Build(&a.Spec, a.Exchanges())
	if err != nil {
		return nil, fmt.Errorf("build recorded mappings: %w", err)
	}
	return s.keep(ctx, sc, &a.Spec, ms, files)
}

// Snapshot builds mappings from the proxied requests in a scope's journal.
func (s *Service) Snapshot(ctx context.Context, sc store.Scope, spec record.Spec) ([]model.StubMapping, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	var filter *match.RequestMatcher
	if spec.Filters != nil {
		var err error
		if filter, err = s.CompilePattern(spec.Filters); err != nil {
			return nil, err
		}
	}
	entries := s.Journal.Query(sc, func(e *journal.Entry) bool {
		return e.Definition != nil && e.Definition.ProxyBaseURL != "" && e.Request != nil &&
			(filter == nil || filter.Match(e.Request))
	}, 0)
	// The journal lists the newest first; mappings are built in the order requests arrived.
	exchanges := make([]record.Exchange, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		// The journal holds the reply as sent, which says which mapping matched; upstream did not say so.
		header := e.Response.Header.Clone()
		header.Del(matchedStubHeader)
		header.Del(matchedNameHeader)
		exchanges = append(exchanges, record.Exchange{Request: e.Request, Status: e.Response.Status, Header: header, Body: e.Response.Body})
	}
	ms, files, err := record.Build(&spec, exchanges)
	if err != nil {
		return nil, fmt.Errorf("build snapshot mappings: %w", err)
	}
	return s.keep(ctx, sc, &spec, ms, files)
}

// putFiles stores the bodies a recording kept out of its mappings.
func (s *Service) putFiles(ctx context.Context, sc store.Scope, files []record.File) error {
	for _, f := range files {
		if err := s.Files.Put(ctx, sc, f.Name, f.Body); err != nil {
			return fmt.Errorf("store recorded body %s: %w", f.Name, err)
		}
	}
	return nil
}

// keep stores recorded mappings and their body files when the spec persists them, and returns the
// mappings as stored, newest first as WireMock lists them.
func (s *Service) keep(ctx context.Context, sc store.Scope, spec *record.Spec, ms []model.StubMapping, files []record.File) ([]model.StubMapping, error) {
	if spec.PersistOrDefault() {
		if err := s.putFiles(ctx, sc, files); err != nil {
			return nil, err
		}
		for i := range ms {
			stored, err := s.PutMapping(ctx, sc, ms[i])
			if err != nil {
				return nil, err
			}
			ms[i] = stored
		}
	}
	slices.Reverse(ms)
	return ms, nil
}

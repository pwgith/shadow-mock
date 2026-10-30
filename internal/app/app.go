// Package app wires the engine together from configuration: stores, the service, the admin
// API and the router. cmd/shadowmock runs it as a process; the acceptance harness runs it
// in-process with a test clock.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/pwgith/shadow-mock/internal/admin"
	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/detect"
	"github.com/pwgith/shadow-mock/internal/egress"
	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/metrics"
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/proxy"
	"github.com/pwgith/shadow-mock/internal/ratelimit"
	"github.com/pwgith/shadow-mock/internal/record"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/server"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/fs"
	"github.com/pwgith/shadow-mock/internal/store/memory"
	"github.com/pwgith/shadow-mock/internal/telemetry"
	"github.com/pwgith/shadow-mock/internal/tenant"
	"github.com/pwgith/shadow-mock/internal/webhook"
	"github.com/pwgith/shadow-mock/pkg/extension"
)

// Options are the process-level dependencies of the engine.
type Options struct {
	Logger *slog.Logger
	Clock  clock.Clock
	// Shutdown is called by POST /__admin/shutdown. Nil disables the endpoint.
	Shutdown func()
	Version  string
	// Matchers are custom request matchers that mappings can name (FR-2.7).
	Matchers []extension.RequestMatcher
	// TracerProvider, when set, receives the spans instead of the OTLP exporter the configuration
	// describes. Tests give one that keeps them in memory; the caller owns it.
	TracerProvider trace.TracerProvider
}

// App is the wired engine.
type App struct {
	cfg       config.Config
	svc       *mock.Service
	mockRoute *server.Router
	metrics   *metrics.Metrics
	tel       *telemetry.Telemetry
	adminOnly *server.Router // set when admin has its own listener
	globalFS  *fs.Mappings
	sessions  *fs.SessionStore
	closers   []func() error
}

// New wires the engine from configuration. Nothing listens or loads until Start.
func New(cfg config.Config, o Options) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	a := &App{cfg: cfg}
	svc, err := a.service(cfg, o)
	if err != nil {
		return nil, errors.Join(err, a.Close())
	}
	a.svc = svc
	if err := a.routers(cfg, o); err != nil {
		return nil, errors.Join(err, a.Close())
	}
	return a, nil
}

func (a *App) service(cfg config.Config, o Options) (*mock.Service, error) {
	custom, err := customMatchers(o.Matchers)
	if err != nil {
		return nil, err
	}
	global, globalFiles, err := a.globalStores(cfg, o.Logger)
	if err != nil {
		return nil, err
	}
	var sessionMappings store.MappingRepository = memory.NewMappings()
	if cfg.Storage.Sessions.Driver == "fs" {
		ss, serr := fs.NewSessionStore(filepath.Join(cfg.Root, "sessions"))
		if serr != nil {
			return nil, serr
		}
		a.sessions = ss
		a.closers = append(a.closers, ss.Close)
		sessionMappings = ss
	}
	mappings := store.SplitMappings{Global: global, Sessions: sessionMappings}
	files := store.SplitFiles{Global: globalFiles, Sessions: memory.NewFiles()}
	limits := session.Limits{
		KeyBasedDefaultTTL: time.Duration(cfg.Sessions.DefaultTTL), KeyBasedMaxTTL: time.Duration(cfg.Sessions.MaxTTL),
		URLBasedDefaultTTL: time.Duration(cfg.Sessions.URLBased.DefaultTTL), URLBasedMaxTTL: time.Duration(cfg.Sessions.URLBased.MaxTTL),
		MinTokenLength: cfg.Sessions.Detect.MinTokenLength, KeyPrefix: cfg.Sessions.KeyPrefix, URLMockIDLength: cfg.Sessions.URLBased.IDLength,
	}
	registry := session.NewRegistry(o.Clock, limits)
	detector := newDetector(cfg.Sessions.Detect)
	detector.SetSource(registry.All)
	tel, err := a.startTelemetry(cfg, o)
	if err != nil {
		return nil, err
	}
	mx := metrics.New(func() int { return len(registry.All()) })
	detector.Observe(o.Clock.Now, mx.Rebuilt)
	a.metrics = mx
	renderer := newRenderer(cfg, files, o.Clock)
	client, forwarder, err := newForwarder(cfg, tel)
	if err != nil {
		return nil, err
	}
	if err := a.nameFiles(cfg.Storage.Global.FilenameTemplate, renderer); err != nil {
		return nil, err
	}
	hooks := webhook.New(client, renderer.Templates, o.Clock, o.Logger)
	a.closers = append(a.closers, func() error { hooks.Close(); return nil })
	jrnl := journal.NewMemory(cfg.Storage.Journal.Hot.MaxEntries)
	jrnl.LimitBodies(cfg.Storage.Journal.Hot.MaxBodyBytes)
	jrnl.LimitSize(cfg.Storage.Journal.Hot.MaxBytes)
	svc := &mock.Service{
		Mappings:  mappings,
		Files:     files,
		Sessions:  registry,
		Layers:    engine.NewManager(mappings, registry, match.Options{Now: o.Clock.Now, CustomMatchers: custom}, o.Logger),
		Detector:  detector,
		Counters:  counter.NewMemory(),
		Journal:   jrnl,
		Renderer:  renderer,
		Webhooks:  hooks,
		Proxy:     forwarder,
		Recorder:  record.NewRecorder(),
		Stats:     stats.New(),
		Metrics:   mx,
		Telemetry: tel,
		Clock:     o.Clock,
		Logger:    o.Logger,
		Config: mock.Config{
			MaxBodyBytes: cfg.Limits.MaxBodyBytes, MappingSetsDir: filepath.Join(cfg.Root, "mapping-sets"),
			MatchedHeader: cfg.Respond.MatchedHeader, LogRequests: cfg.Log.Requests,
			KeepUnknownFields: cfg.Mappings.UnknownFields == "keep",
			StubCORS:          cfg.Server.StubCORS, ServeFiles: cfg.Server.ServeFiles,
			Limits: mock.Limits{
				MaxSessions: cfg.Limits.MaxSessions, MaxMappings: cfg.Limits.MaxMappings,
				MaxMappingsPerSession: cfg.Limits.MaxMappingsPerSession, MaxDelay: time.Duration(cfg.Limits.MaxDelay),
			},
		},
	}
	if a.sessions != nil {
		svc.SessionStore = a.sessions
	}
	return svc, nil
}

// newDetector builds the session-key detector from the sessions.detect settings.
func newDetector(d config.Detect) *detect.Index {
	return detect.New(detect.Config{
		Headers: d.Headers, QueryParams: d.QueryParams, Cookies: d.Cookies, ScanMetadata: d.ScanMetadata,
		ScanBody: d.ScanBody, MaxScanBytes: d.MaxScanBytes, ContentTypes: d.ContentTypes, Decompress: d.Decompress,
		Ambiguity: detect.Policy(d.Ambiguity),
	})
}

func (a *App) routers(cfg config.Config, o Options) error {
	allow, err := server.ParsePrefixes(cfg.Tenancy.Local.IPAllowlist)
	if err != nil {
		return fmt.Errorf("tenancy.local.ipAllowlist: %w", err)
	}
	proxies, err := server.ParsePrefixes(cfg.Server.ClientIP.TrustedProxies)
	if err != nil {
		return fmt.Errorf("server.clientIp.trustedProxies: %w", err)
	}
	var limiter *ratelimit.Limiter
	if cfg.Limits.RequestsPerSecond > 0 {
		limiter = ratelimit.New(o.Clock, cfg.Limits.RequestsPerSecond, cfg.Limits.Burst)
	}
	authn, guard := newAuthenticators(cfg)
	adminAPI := admin.New(a.svc, authn, auth.RoleAuthorizer{DenyGlobalWrites: cfg.Admin.GlobalWrites == "deny"},
		admin.Config{Version: o.Version, PublicBaseURL: cfg.Server.PublicBaseURL, MaxBodyBytes: cfg.Limits.MaxBodyBytes, Shutdown: o.Shutdown}, o.Logger)
	route := server.Router{
		Directory:    tenant.NewStatic(&tenant.Org{ID: tenant.Local, Handle: string(tenant.Local), Allowlist: allow, AllowlistAdmin: cfg.Tenancy.Local.AllowlistAdmin}),
		LocalOrg:     string(tenant.Local),
		AdminPrefix:  cfg.AdminPrefix,
		Admin:        adminAPI,
		Service:      a.svc,
		Stats:        a.svc.Stats,
		ClientIP:     server.ClientIP{Header: cfg.Server.ClientIP.Header, TrustedHops: cfg.Server.ClientIP.TrustedHops, TrustedProxies: proxies},
		ServeMock:    true,
		ServeAdmin:   cfg.Listen.Admin == "",
		Metrics:      a.metrics.Handler(),
		MetricsAuth:  guard,
		Telemetry:    a.tel,
		Limiter:      limiter,
		AdminHosts:   cfg.Admin.AllowedHosts,
		AdminOrigins: cfg.Admin.AllowedOrigins,

		AdminRequireHTTPS: cfg.TLS.AdminRequireHTTPS,
	}
	a.mockRoute = &route
	if cfg.Listen.Admin != "" {
		adminRoute := route
		adminRoute.ServeMock, adminRoute.ServeAdmin = false, true
		a.adminOnly = &adminRoute
		a.mockRoute.Metrics = nil
	}
	return nil
}

func (a *App) globalStores(cfg config.Config, logger *slog.Logger) (store.MappingRepository, store.FileRepository, error) {
	if cfg.Storage.Global.Driver == "memory" {
		return memory.NewMappings(), memory.NewFiles(), nil
	}
	global, err := fs.NewMappings(filepath.Join(cfg.Root, "mappings"), logger)
	if err != nil {
		return nil, nil, err
	}
	a.globalFS = global
	a.closers = append(a.closers, global.Close)
	files, err := fs.NewFiles(filepath.Join(cfg.Root, "__files"))
	if err != nil {
		return nil, nil, err
	}
	a.closers = append(a.closers, files.Close)
	return global, files, nil
}

// Start loads mappings and persisted sessions, then starts the file watcher and the session
// reaper. They stop when ctx is done.
func (a *App) Start(ctx context.Context) error {
	local := store.Global(tenant.Local)
	var changes <-chan store.ChangeEvent
	if a.globalFS != nil {
		if err := a.globalFS.Load(ctx, local); err != nil {
			return fmt.Errorf("load mappings: %w", err)
		}
		if a.cfg.Storage.Global.Watch {
			ch, err := a.globalFS.Watch(ctx, filepath.Join(a.cfg.Root, "mappings"), local)
			if err != nil {
				return err
			}
			changes = ch
		}
	}
	if err := a.svc.Start(ctx, []tenant.ID{tenant.Local}, changes); err != nil {
		return err
	}
	if a.sessions != nil {
		restored, err := a.sessions.LoadSessions(ctx)
		if err != nil {
			return err
		}
		if err := a.svc.RestoreSessions(ctx, restored); err != nil {
			return err
		}
	}
	go a.svc.Reap(ctx, time.Second)
	return a.startProxyAll(ctx)
}

// nameFiles gives the global mappings store a filename template, after checking it renders.
func (a *App) nameFiles(tpl string, r *respond.Renderer) error {
	if tpl == "" || a.globalFS == nil {
		return nil
	}
	namer := func(data map[string]any) (string, error) { return r.Templates.Render(tpl, data) }
	if _, err := namer(fs.FilenameData(model.StubMapping{ID: "00000000-0000-4000-8000-000000000000"})); err != nil {
		return fmt.Errorf("storage.global.filenameTemplate: %w", err)
	}
	a.globalFS.SetNamer(namer)
	return nil
}

// proxyAllID is the id of the mapping --proxy-all adds.
const proxyAllID = "00000000-0000-4000-8000-0000000000a1"

// startProxyAll adds the mapping that sends every unmatched request to proxy.all, and with
// proxy.record starts recording what comes back. Each new request is written to mappings/ as soon
// as it has been proxied.
func (a *App) startProxyAll(ctx context.Context) error {
	if a.cfg.Proxy.All == "" {
		return nil
	}
	local := store.Global(tenant.Local)
	def, err := json.Marshal(map[string]any{
		"id": proxyAllID, "priority": 10,
		"request": map[string]any{"method": "ANY"}, "response": map[string]any{"proxyBaseUrl": a.cfg.Proxy.All},
	})
	if err != nil {
		return fmt.Errorf("proxy.all: %w", err)
	}
	m, err := a.svc.DecodeAndValidate(def)
	if err != nil {
		return fmt.Errorf("proxy.all: %w", err)
	}
	if _, err := a.svc.PutMapping(ctx, local, m); err != nil {
		return fmt.Errorf("proxy.all: %w", err)
	}
	if !a.cfg.Proxy.Record {
		return nil
	}
	// Like WireMock, every recorded body goes to a file in __files.
	spec := record.Spec{
		TargetBaseURL:       a.cfg.Proxy.All,
		CaptureHeaders:      map[string]record.CaptureHeader{},
		ExtractBodyCriteria: &record.ExtractBody{TextSizeThreshold: "0 kb", BinarySizeThreshold: "0 kb"},
	}
	for _, h := range a.cfg.Proxy.MatchHeaders {
		spec.CaptureHeaders[h] = record.CaptureHeader{}
	}
	if err := a.svc.StartLiveRecording(ctx, local, spec); err != nil {
		return fmt.Errorf("proxy.record: %w", err)
	}
	return nil
}

// ReloadMappings reloads the global mapping files, as the watcher does after a change.
func (a *App) ReloadMappings(ctx context.Context) error {
	local := store.Global(tenant.Local)
	if a.globalFS != nil {
		if err := a.globalFS.Load(ctx, local); err != nil {
			return fmt.Errorf("reload mappings: %w", err)
		}
	}
	return a.svc.Layers.Rebuild(ctx, local)
}

// Close releases file handles.
func (a *App) Close() error {
	errs := make([]error, 0, len(a.closers))
	for _, c := range a.closers {
		errs = append(errs, c())
	}
	a.closers = nil
	return errors.Join(errs...)
}

// Service returns the service, for tests and the acceptance harness.
func (a *App) Service() *mock.Service { return a.svc }

// Handler returns the handler for mock traffic (and admin, unless it has its own listener).
func (a *App) Handler() http.Handler { return a.mockRoute }

// Handlers returns the handler for each listener address.
func (a *App) Handlers() map[string]http.Handler {
	out := map[string]http.Handler{a.cfg.Listen.Mock: a.mockRoute}
	if a.adminOnly != nil {
		out[a.cfg.Listen.Admin] = a.adminOnly
	}
	return out
}

// proxyWithTrace returns a forwarder whose upstream calls continue the caller's trace.
// customMatchers indexes the custom matchers by name.
func customMatchers(matchers []extension.RequestMatcher) (map[string]extension.RequestMatcher, error) {
	custom := make(map[string]extension.RequestMatcher, len(matchers))
	for _, m := range matchers {
		if _, dup := custom[m.Name()]; dup {
			return nil, fmt.Errorf("two custom matchers are named %q", m.Name())
		}
		custom[m.Name()] = m
	}
	return custom, nil
}

// newAuthenticators picks the admin authenticator. guard is set when the admin API and the metrics
// need a login.
func newAuthenticators(cfg config.Config) (authn, guard auth.Authenticator) {
	switch cfg.Auth.Driver {
	case "basic":
		authn = auth.NewBasicAuthenticator(cfg.Auth.Basic.Username, cfg.Auth.Basic.Password)
		return authn, authn
	case "token":
		tokens := make([]auth.Token, len(cfg.Auth.Tokens))
		for i, t := range cfg.Auth.Tokens {
			tokens[i] = auth.Token{Name: t.Name, Role: auth.Role(t.Role), Hash: t.Hash}
		}
		authn = auth.NewTokenAuthenticator(tokens)
		return authn, authn
	}
	return auth.NoopAuthenticator{}, nil
}

// newRenderer builds the response renderer under the respond, templating and proxy settings.
func newRenderer(cfg config.Config, files store.FileRepository, clk clock.Clock) *respond.Renderer {
	renderer := respond.NewRenderer(files, clk, cfg.Respond.Gzip != "off")
	renderer.Templating = cfg.Templating.Global
	renderer.TemplatingDisabled = cfg.Templating.Disabled
	if cfg.Proxy.PassThrough != nil {
		renderer.SetSettings(respond.Settings{ProxyPassThrough: *cfg.Proxy.PassThrough})
	}
	renderer.Templates.PermitSystemKeys(cfg.Templating.PermittedSystemKeys)
	renderer.MaxDelay = time.Duration(cfg.Limits.MaxDelay)
	return renderer
}

// newForwarder builds the outbound client, and the forwarder that proxies through it under the
// proxy settings.
func newForwarder(cfg config.Config, tel *telemetry.Telemetry) (*http.Client, *proxy.Forwarder, error) {
	opts := egress.Options{BlockPrivate: cfg.Egress.BlockPrivate, Timeout: time.Duration(cfg.Proxy.Timeout)}
	if cfg.Proxy.Via != "" {
		via, err := egress.ViaProxy(cfg.Proxy.Via)
		if err != nil {
			return nil, nil, fmt.Errorf("proxy.via: %w", err)
		}
		opts.Proxy = via
	}
	targets, err := egress.ParseTargets(cfg.Proxy.AllowTargets, cfg.Proxy.DenyTargets)
	if err != nil {
		return nil, nil, fmt.Errorf("proxy targets: %w", err)
	}
	client := egress.New(opts)
	forwarder := proxyWithTrace(client, tel)
	forwarder.Targets, forwarder.PreserveHost = targets, cfg.Proxy.PreserveHostHeader
	return client, forwarder, nil
}

func proxyWithTrace(client *http.Client, tel *telemetry.Telemetry) *proxy.Forwarder {
	f := proxy.New(client)
	f.Inject = tel.Inject
	return f
}

// startTelemetry sets up tracing, and flushes it when the app closes.
func (a *App) startTelemetry(cfg config.Config, o Options) (*telemetry.Telemetry, error) {
	tel, err := telemetry.New(context.Background(), telemetry.Config{
		Enabled: cfg.Tracing.Enabled, Endpoint: cfg.Tracing.Endpoint, ServiceName: cfg.Tracing.ServiceName, SampleRatio: cfg.Tracing.SampleRatio,
	}, o.Version, o.TracerProvider)
	if err != nil {
		return nil, err
	}
	a.tel = tel
	a.closers = append(a.closers, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return tel.Shutdown(ctx)
	})
	return tel, nil
}

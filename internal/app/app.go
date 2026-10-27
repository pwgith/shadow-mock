// Package app wires the engine together from configuration: stores, the service, the admin
// API and the router. cmd/shadowmock runs it as a process; the acceptance harness runs it
// in-process with a test clock.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

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
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/proxy"
	"github.com/pwgith/shadow-mock/internal/record"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/server"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/fs"
	"github.com/pwgith/shadow-mock/internal/store/memory"
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
}

// App is the wired engine.
type App struct {
	cfg       config.Config
	svc       *mock.Service
	mockRoute *server.Router
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
	custom := make(map[string]extension.RequestMatcher, len(o.Matchers))
	for _, m := range o.Matchers {
		if _, dup := custom[m.Name()]; dup {
			return nil, fmt.Errorf("two custom matchers are named %q", m.Name())
		}
		custom[m.Name()] = m
	}
	global, globalFiles, err := a.globalStores(cfg, o.Logger)
	if err != nil {
		return nil, err
	}
	var sessionMappings store.MappingRepository = memory.NewMappings()
	if cfg.Storage.Sessions.Driver == "fs" {
		ss, err := fs.NewSessionStore(filepath.Join(cfg.Root, "sessions"))
		if err != nil {
			return nil, err
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
	d := cfg.Sessions.Detect
	detector := detect.New(detect.Config{
		Headers: d.Headers, QueryParams: d.QueryParams, Cookies: d.Cookies, ScanMetadata: d.ScanMetadata,
		ScanBody: d.ScanBody, MaxScanBytes: d.MaxScanBytes, ContentTypes: d.ContentTypes, Decompress: d.Decompress,
		Ambiguity: detect.Policy(d.Ambiguity),
	})
	detector.SetSource(registry.All)
	renderer := respond.NewRenderer(files, o.Clock, cfg.Respond.Gzip != "off")
	renderer.Templating = cfg.Templating.Global
	client := egress.New(egress.Options{BlockPrivate: cfg.Egress.BlockPrivate})
	hooks := webhook.New(client, renderer.Templates, o.Clock, o.Logger)
	a.closers = append(a.closers, func() error { hooks.Close(); return nil })
	jrnl := journal.NewMemory(cfg.Storage.Journal.Hot.MaxEntries)
	jrnl.LimitBodies(cfg.Storage.Journal.Hot.MaxBodyBytes)
	svc := &mock.Service{
		Mappings: mappings,
		Files:    files,
		Sessions: registry,
		Layers:   engine.NewManager(mappings, registry, match.Options{Now: o.Clock.Now, CustomMatchers: custom}, o.Logger),
		Detector: detector,
		Counters: counter.NewMemory(),
		Journal:  jrnl,
		Renderer: renderer,
		Webhooks: hooks,
		Proxy:    proxy.New(client),
		Recorder: record.NewRecorder(),
		Stats:    stats.New(),
		Clock:    o.Clock,
		Logger:   o.Logger,
		Config: mock.Config{
			MaxBodyBytes: cfg.Limits.MaxBodyBytes, MappingSetsDir: filepath.Join(cfg.Root, "mapping-sets"),
			MatchedHeader: cfg.Respond.MatchedHeader,
		},
	}
	if a.sessions != nil {
		svc.SessionStore = a.sessions
	}
	return svc, nil
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
	adminAPI := admin.New(a.svc, auth.NoopAuthenticator{}, auth.RoleAuthorizer{DenyGlobalWrites: cfg.Admin.GlobalWrites == "deny"},
		admin.Config{Version: o.Version, PublicBaseURL: cfg.Server.PublicBaseURL, MaxBodyBytes: cfg.Limits.MaxBodyBytes, Shutdown: o.Shutdown}, o.Logger)
	route := server.Router{
		Directory:   tenant.NewStatic(&tenant.Org{ID: tenant.Local, Handle: string(tenant.Local), Allowlist: allow}),
		LocalOrg:    string(tenant.Local),
		AdminPrefix: cfg.AdminPrefix,
		Admin:       adminAPI,
		Service:     a.svc,
		Stats:       a.svc.Stats,
		ClientIP:    server.ClientIP{Header: cfg.Server.ClientIP.Header, TrustedHops: cfg.Server.ClientIP.TrustedHops, TrustedProxies: proxies},
		ServeMock:   true,
		ServeAdmin:  cfg.Listen.Admin == "",
		Metrics:     server.WriteMetrics(a.svc),
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

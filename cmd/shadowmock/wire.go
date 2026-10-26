package main

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
	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/respond"
	"github.com/pwgith/shadow-mock/internal/server"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/stats"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/fs"
	"github.com/pwgith/shadow-mock/internal/store/memory"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// app is the wired engine.
type app struct {
	svc       *mock.Service
	mockRoute *server.Router
	adminOnly *server.Router // set when admin has its own listener
	closers   []func() error
	globalFS  *fs.Mappings
	cfg       config.Config
}

// build wires the engine from configuration. shutdown is called by POST /__admin/shutdown.
func build(cfg config.Config, logger *slog.Logger, clk clock.Clock, shutdown func()) (*app, error) {
	a := &app{cfg: cfg}
	global, globalFiles, err := a.globalStores(cfg, logger)
	if err != nil {
		return nil, errors.Join(err, a.close())
	}
	mappings := store.SplitMappings{Global: global, Sessions: memory.NewMappings()}
	files := store.SplitFiles{Global: globalFiles, Sessions: memory.NewFiles()}

	limits := session.Limits{
		KeyBasedDefaultTTL: time.Duration(cfg.Sessions.DefaultTTL), KeyBasedMaxTTL: time.Duration(cfg.Sessions.MaxTTL),
		URLBasedDefaultTTL: time.Duration(cfg.Sessions.URLBased.DefaultTTL), URLBasedMaxTTL: time.Duration(cfg.Sessions.URLBased.MaxTTL),
		MinTokenLength: cfg.Sessions.Detect.MinTokenLength, KeyPrefix: cfg.Sessions.KeyPrefix, URLMockIDLength: cfg.Sessions.URLBased.IDLength,
	}
	registry := session.NewRegistry(clk, limits)
	d := cfg.Sessions.Detect
	svc := &mock.Service{
		Mappings: mappings,
		Files:    files,
		Sessions: registry,
		Layers:   engine.NewManager(mappings, registry, match.Options{Now: clk.Now}, logger),
		Detector: detect.New(detect.Config{
			Headers: d.Headers, QueryParams: d.QueryParams, Cookies: d.Cookies, ScanMetadata: d.ScanMetadata,
			ScanBody: d.ScanBody, MaxScanBytes: d.MaxScanBytes, ContentTypes: d.ContentTypes, Decompress: d.Decompress,
			Ambiguity: detect.Policy(d.Ambiguity),
		}),
		Counters: counter.NewMemory(),
		Journal:  journal.NewMemory(cfg.Journal.MaxEntries),
		Renderer: respond.NewRenderer(files, clk, cfg.Respond.Gzip != "off"),
		Stats:    stats.New(),
		Clock:    clk,
		Logger:   logger,
		Config: mock.Config{
			MaxBodyBytes: cfg.Limits.MaxBodyBytes, MappingSetsDir: filepath.Join(cfg.Root, "mapping-sets"),
			MatchedHeader: cfg.Respond.MatchedHeader,
		},
	}
	a.svc = svc

	allow, err := server.ParsePrefixes(cfg.Tenancy.Local.IPAllowlist)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("tenancy.local.ipAllowlist: %w", err), a.close())
	}
	proxies, err := server.ParsePrefixes(cfg.Server.ClientIP.TrustedProxies)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("server.clientIp.trustedProxies: %w", err), a.close())
	}
	adminAPI := admin.New(svc, auth.NoopAuthenticator{}, auth.RoleAuthorizer{DenyGlobalWrites: cfg.Admin.GlobalWrites == "deny"},
		admin.Config{Version: version, PublicBaseURL: cfg.Server.PublicBaseURL, MaxBodyBytes: cfg.Limits.MaxBodyBytes, Shutdown: shutdown}, logger)
	route := server.Router{
		Directory: tenant.NewStatic(&tenant.Org{ID: tenant.Local, Handle: string(tenant.Local), Allowlist: allow}),
		LocalOrg:  string(tenant.Local),
		Admin:     adminAPI,
		Service:   svc,
		Stats:     svc.Stats,
		ClientIP:  server.ClientIP{Header: cfg.Server.ClientIP.Header, TrustedHops: cfg.Server.ClientIP.TrustedHops, TrustedProxies: proxies},
		ServeMock: true, ServeAdmin: cfg.Listen.Admin == "",
		Metrics: server.WriteMetrics(svc),
	}
	a.mockRoute = &route
	if cfg.Listen.Admin != "" {
		adminRoute := route
		adminRoute.ServeMock, adminRoute.ServeAdmin = false, true
		a.adminOnly = &adminRoute
		a.mockRoute.Metrics = nil
	}
	return a, nil
}

func (a *app) globalStores(cfg config.Config, logger *slog.Logger) (store.MappingRepository, store.FileRepository, error) {
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

// start loads the global mappings, starts the file watcher and the session reaper.
func (a *app) start(ctx context.Context) error {
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
	go a.svc.Reap(ctx, time.Second)
	return nil
}

func (a *app) close() error {
	errs := make([]error, 0, len(a.closers))
	for _, c := range a.closers {
		errs = append(errs, c())
	}
	return errors.Join(errs...)
}

// handlers returns the handler for each listener address.
func (a *app) handlers() map[string]http.Handler {
	out := map[string]http.Handler{a.cfg.Listen.Mock: a.mockRoute}
	if a.adminOnly != nil {
		out[a.cfg.Listen.Admin] = a.adminOnly
	}
	return out
}

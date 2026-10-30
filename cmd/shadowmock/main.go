// Command shadowmock runs the Shadow Mock engine: a WireMock-compatible mock server with
// key-based and URL-based shadow mocks (docs/REQUIREMENTS.md).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/netutil"

	"golang.org/x/sync/errgroup"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/server"
	"github.com/pwgith/shadow-mock/internal/telemetry"
	"github.com/pwgith/shadow-mock/internal/tlsconf"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "0.0.0-dev"

// maxDelay bounds the write timeout; mock delays longer than this are cut off.
const maxDelay = 5 * time.Minute

func main() {
	if err := run(os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		slog.Error("shadowmock stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(args []string, lookup func(string) (string, bool), stdout io.Writer) error {
	cfg, flags, err := config.Resolve(args, lookup)
	if err != nil {
		return err
	}
	if flags.ShowVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}
	if flags.HealthCheck {
		return healthCheck(context.Background(), cfg.Listen.Mock, &http.Client{})
	}
	logger := newLogger(cfg.Log)
	for _, w := range flags.Warnings {
		logger.Warn("command line", "warning", w)
	}
	return serve(cfg, logger)
}

// healthCheck asks the server listening on addr for /__health. It exists so a container, which has
// no curl, can check itself: docker runs "shadowmock -healthcheck" with the container's settings.
func healthCheck(ctx context.Context, addr string, client *http.Client) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/__health", http.NoBody)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: %s", resp.Status)
	}
	return nil
}

func newLogger(c config.Log) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Level)); err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(c.Format, "json") {
		return slog.New(telemetry.NewLogHandler(slog.NewJSONHandler(os.Stderr, opts)))
	}
	return slog.New(telemetry.NewLogHandler(slog.NewTextHandler(os.Stderr, opts)))
}

// endpoint is one listener: an address, what it serves, and the TLS settings when it is HTTPS.
type endpoint struct {
	addr    string
	handler http.Handler
	tls     *tls.Config
}

// endpoints lists the listeners to open: the plain ones (unless --disable-http) and, when
// tls.port is set, the HTTPS listener that serves what the mock listener serves.
func endpoints(cfg config.Config, a *app.App) ([]endpoint, error) {
	var out []endpoint
	for addr, h := range a.Handlers() {
		if cfg.TLS.DisableHTTP && addr == cfg.Listen.Mock {
			continue
		}
		out = append(out, endpoint{addr: addr, handler: h})
	}
	if cfg.TLS.Enabled() {
		tc, err := tlsconf.Config(cfg.TLS, clock.Real{})
		if err != nil {
			return nil, err
		}
		out = append(out, endpoint{addr: cfg.HTTPSAddr(), handler: a.Handler(), tls: tc})
	}
	return out, nil
}

func serve(cfg config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveUntil(ctx.Done(), stop, cfg, logger)
}

// serveUntil serves until done closes. stop is what POST /__admin/shutdown calls to end it.
func serveUntil(done <-chan struct{}, stop func(), cfg config.Config, logger *slog.Logger) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()
	for _, w := range cfg.Warnings() {
		logger.WarnContext(ctx, "configuration", "warning", w)
	}
	if cfg.Auth.Driver == "token" {
		logger.InfoContext(ctx, "admin authentication is on", "tokens", len(cfg.Auth.Tokens), "mode", cfg.Mode)
	}
	a, err := app.New(cfg, app.Options{Logger: logger, Clock: clock.Real{}, Shutdown: stop, Version: version})
	if err != nil {
		return err
	}
	defer a.Close()
	if startErr := a.Start(ctx); startErr != nil {
		return startErr
	}
	g, gctx := errgroup.WithContext(ctx)
	eps, err := endpoints(cfg, a)
	if err != nil {
		return err
	}
	for _, ep := range eps {
		if startErr := startEndpoint(gctx, g, ep, cfg, logger); startErr != nil {
			return startErr
		}
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	logger.InfoContext(ctx, "stopped")
	return nil
}

// startEndpoint opens a listener and serves it in g until gctx ends, then shuts it down.
func startEndpoint(gctx context.Context, g *errgroup.Group, ep endpoint, cfg config.Config, logger *slog.Logger) error {
	srv := server.New(ep.addr, ep.handler, maxDelay)
	server.Protocols(srv, !cfg.TLS.DisableHTTP2TLS, !cfg.TLS.DisableHTTP2Plain)
	srv.TLSConfig = ep.tls
	ln, err := listen(gctx, ep.addr, cfg.Limits.MaxConnections)
	if err != nil {
		return err
	}
	g.Go(func() error {
		logger.InfoContext(gctx, "listening", "addr", ep.addr, "https", ep.tls != nil, "root", cfg.Root, "max_connections", cfg.Limits.MaxConnections)
		var serveErr error
		if ep.tls != nil {
			serveErr = srv.ServeTLS(ln, "", "")
		} else {
			serveErr = srv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve on %s: %w", ep.addr, serveErr)
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(gctx), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down %s: %w", ep.addr, err)
		}
		return nil
	})
	return nil
}

// listen opens a TCP listener. With max above zero it holds at most that many connections open:
// another waits in the kernel's queue until one closes, so a flood of connections cannot take the
// server's memory or file descriptors.
func listen(ctx context.Context, addr string, maxConns int) (net.Listener, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	if maxConns > 0 {
		return netutil.LimitListener(ln, maxConns), nil
	}
	return ln, nil
}

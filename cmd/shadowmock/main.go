// Command shadowmock runs the Shadow Mock engine: a WireMock-compatible mock server with
// key-based and URL-based shadow mocks (docs/REQUIREMENTS.md).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/pwgith/shadow-mock/internal/app"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/server"
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
	return serve(cfg, newLogger(cfg.Log))
}

func newLogger(c config.Log) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Level)); err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(c.Format, "json") {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func serve(cfg config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	if err := a.Start(ctx); err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	for addr, h := range a.Handlers() {
		srv := server.New(addr, h, maxDelay)
		g.Go(func() error {
			logger.InfoContext(gctx, "listening", "addr", addr, "root", cfg.Root)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("listen on %s: %w", addr, err)
			}
			return nil
		})
		g.Go(func() error {
			<-gctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(gctx), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("shut down %s: %w", addr, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	logger.InfoContext(ctx, "stopped")
	return nil
}

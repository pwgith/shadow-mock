// Command shadowmock runs the Shadow Mock engine: a WireMock-compatible mock server with
// key-based and URL-based shadow mocks (docs/REQUIREMENTS.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/server"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "0.1.0-dev"

// maxDelay bounds the write timeout; mock delays longer than this are cut off.
const maxDelay = 5 * time.Minute

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("shadowmock stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fl := flag.NewFlagSet("shadowmock", flag.ContinueOnError)
	cfgPath := fl.String("config", "", "YAML configuration file")
	port := fl.Int("port", 0, "mock listener port (overrides listen.mock)")
	adminPort := fl.Int("admin-port", 0, "separate admin listener port (overrides listen.admin)")
	root := fl.String("root-dir", "", "directory holding mappings/ and __files/ (overrides root)")
	verbose := fl.Bool("verbose", false, "log at debug level")
	showVersion := fl.Bool("version", false, "print the version and exit")
	if err := fl.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if *showVersion {
		fmt.Fprintln(os.Stdout, version)
		return nil
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *port != 0 {
		cfg.Listen.Mock = fmt.Sprintf(":%d", *port)
	}
	if *adminPort != 0 {
		cfg.Listen.Admin = fmt.Sprintf(":%d", *adminPort)
	}
	if *root != "" {
		cfg.Root = *root
	}
	if *verbose {
		cfg.Log.Level = "debug"
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
	a, err := build(cfg, logger, clock.Real{}, stop)
	if err != nil {
		return err
	}
	defer a.close()
	if err := a.start(ctx); err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	for addr, h := range a.handlers() {
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

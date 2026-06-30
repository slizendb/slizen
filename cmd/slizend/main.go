package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/slizendb/slizen/internal/admin"
	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/metrics"
	"github.com/slizendb/slizen/internal/proxy"
	"github.com/slizendb/slizen/internal/service"
	"github.com/slizendb/slizen/internal/upstream"
)

var version = "0.1.0"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("slizend", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to slizen TOML configuration")
	showVersion := fs.Bool("version", false, "print version and exit")
	checkConfig := fs.Bool("check-config", false, "validate configuration and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *checkConfig {
		_, _ = fmt.Fprintln(stdout, "configuration ok")
		return nil
	}

	logger := newLogger(cfg, stderr)
	logger.Info("starting slizend", "version", version, "config", config.RedactedSummary(cfg))

	upstreamClient := upstream.NewRedisClient(cfg.Upstream)
	pingCtx, cancelPing := context.WithTimeout(context.Background(), cfg.Upstream.DialTimeout)
	if err := upstreamClient.Ping(pingCtx); err != nil {
		cancelPing()
		_ = upstreamClient.Close()
		return fmt.Errorf("upstream is not reachable")
	}
	cancelPing()

	recorder := metrics.New()
	svc := service.New(service.Options{
		Config:   cfg,
		Upstream: upstreamClient,
		Metrics:  recorder,
		Logger:   logger,
		Version:  version,
	})
	defer func() {
		if err := svc.Close(); err != nil {
			logger.Warn("service close failed", "error", err)
		}
	}()

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	serverCtx, cancelServers := context.WithCancel(rootCtx)
	defer cancelServers()

	adminServer := admin.NewServer(cfg.Admin, svc, logger)
	proxyServer := proxy.NewServer(cfg.Proxy, svc, logger)

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- adminServer.Start(serverCtx)
	}()
	go func() {
		defer wg.Done()
		errCh <- proxyServer.Start(serverCtx)
	}()

	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			cancelServers()
			wg.Wait()
			return err
		}
	}

	cancelServers()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(cfg.Proxy.ShutdownTimeout):
		_ = proxyServer.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = adminServer.Close(shutdownCtx)
		cancel()
	}

	logger.Info("slizend stopped")
	return nil
}

func newLogger(cfg config.Config, output io.Writer) *slog.Logger {
	var level slog.Level
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Logging.Format == "text" {
		return slog.New(slog.NewTextHandler(output, opts))
	}
	return slog.New(slog.NewJSONHandler(output, opts))
}

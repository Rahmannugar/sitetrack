package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sitetrack/internal/config"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/infra/telemetry"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "sitetrack-api")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) (resultErr error) {
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid API configuration", "error", err)
		return err
	}
	monitoring, err := telemetry.New(ctx, cfg.Telemetry, "sitetrack-api", os.Stdout)
	if err != nil {
		logger.Error("could not initialize telemetry", "error", err)
		return err
	}
	logger = monitoring.Logger
	defer func() {
		if resultErr != nil {
			logger.Error("API stopped", "error", resultErr)
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := monitoring.Shutdown(shutdown); err != nil {
			slog.New(slog.NewJSONHandler(os.Stderr, nil)).Warn("telemetry shutdown did not complete")
		}
	}()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := database.Open(startup, cfg.DatabaseURL, cfg.DatabasePools.API)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()

	router, err := newRouter(pool, monitoring)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddress(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	logger.Info("API starting", "address", server.Addr)
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		logger.Info("API shutting down")
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP: %w", err)
		}
		return nil
	}
}

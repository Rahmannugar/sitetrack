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

	"github.com/gin-gonic/gin"

	"sitetrack/internal/config"
	"sitetrack/internal/health"
	"sitetrack/internal/infra/database"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "sitetrack-api")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := database.Open(startup, cfg.DatabaseURL, cfg.DatabaseMaxConnections)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		return fmt.Errorf("configure trusted proxies: %w", err)
	}
	router.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		logger.ErrorContext(c.Request.Context(), "request panicked", "route", c.FullPath())
		c.AbortWithStatusJSON(http.StatusInternalServerError, struct {
			Error string `json:"error"`
		}{Error: "internal server error"})
	}))
	health.Register(router, pool)
	server := &http.Server{
		Addr:              cfg.HTTPAddress(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// WriteTimeout stays unset so the later SSE stream is not cut off by a
	// server-wide deadline. Ordinary handlers own their operation deadlines.
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
			// Close active connections before the deferred pool closure.
			_ = server.Close()
			return fmt.Errorf("shut down HTTP: %w", err)
		}
		return nil
	}
}

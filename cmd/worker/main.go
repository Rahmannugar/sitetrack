package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sitetrack/internal/authentication"
	"sitetrack/internal/config"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/infra/email"
	"sitetrack/internal/infra/outbox"
	"sitetrack/internal/infra/telemetry"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "sitetrack-worker")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, bootstrap *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cipher, err := outbox.NewPayloadCipher(cfg.Authentication.OutboxKey)
	if err != nil {
		return err
	}
	monitoring, err := telemetry.New(ctx, cfg.Telemetry, "sitetrack-worker", os.Stdout)
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := monitoring.Shutdown(shutdown); err != nil {
			bootstrap.Warn("worker telemetry shutdown did not complete")
		}
	}()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := database.Open(startup, cfg.DatabaseURL, cfg.DatabasePools.Worker)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()
	var sender email.Sender
	if cfg.Authentication.ResendKey != "" {
		sender, err = email.NewResendSender(cfg.Authentication.ResendKey)
		if err != nil {
			return err
		}
	} else {
		monitoring.Logger.Warn("email processing awaits a Resend API key")
	}
	emails := authentication.NewEmailWorker(pool, cipher, sender, monitoring)
	handlers := make(map[string]outbox.Handler)
	// We leave email jobs queued while the provider is unconfigured.
	if sender != nil {
		handlers["email.verification"] = emails.HandleVerification
		handlers["email.password_reset"] = emails.HandlePasswordReset
	}
	worker, err := outbox.NewWorker(pool, handlers, emails.CleanupOTPs, monitoring)
	if err != nil {
		return err
	}
	monitoring.Logger.Info("worker starting")
	worker.Run(ctx)
	monitoring.Logger.Info("worker stopped")
	return nil
}

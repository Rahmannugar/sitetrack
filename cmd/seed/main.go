package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/knadh/koanf/parsers/dotenv"
	koanfenv "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/seed"
)

func main() {
	profile := flag.String("profile", "full", "full or small")
	envFile := flag.String("env-file", "", "seed-only environment file")
	manifest := flag.String("manifest", "bin/seed-manifest.json", "output manifest path")
	timeout := flag.Duration("timeout", 30*time.Minute, "seed operation timeout")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "sitetrack-seed")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *profile, *envFile, *manifest, *timeout, logger); err != nil {
		logger.Error("seed failed", "error", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, profile, envFile, manifest string, timeout time.Duration, logger *slog.Logger) error {
	if _, err := seed.ProfileByName(profile); err != nil {
		return err
	}
	if timeout <= 0 {
		return errors.New("seed timeout must be positive")
	}
	const prefix = "SITETRACK_SEED_"
	transform := func(key, value string) (string, any) { return strings.ToLower(strings.TrimPrefix(key, prefix)), value }
	k := koanf.New(".")
	if envFile != "" {
		if err := k.Load(file.Provider(envFile), dotenv.ParserEnvWithValue(prefix, ".", transform)); err != nil {
			return errors.New("could not load seed environment file")
		}
	}
	if err := k.Load(koanfenv.Provider(".", koanfenv.Opt{Prefix: prefix, TransformFunc: transform}), nil); err != nil {
		return errors.New("could not load seed environment")
	}
	url, rawPassword := k.String("database_url"), k.String("password")
	if url == "" {
		return errors.New("set SITETRACK_SEED_DATABASE_URL")
	}
	if rawPassword == "" {
		rawPassword = seed.DefaultPassword
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pool, err := database.Open(ctx, url, 1)
	if err != nil {
		return err
	}
	defer pool.Close()
	report, err := seed.Run(ctx, pool, profile, rawPassword, time.Now(), func(p seed.Progress) {
		if p.Rows == p.Total || p.Rows%100000 == 0 {
			logger.Info("seed progress", "phase", p.Phase, "rows", p.Rows, "total", p.Total, "elapsed", p.Elapsed.String())
		}
	})
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return errors.New("seed committed but manifest encoding failed")
	}
	if err := os.MkdirAll(filepath.Dir(manifest), 0755); err != nil {
		return errors.New("seed committed but manifest folder could not be created")
	}
	if err := os.WriteFile(manifest, append(encoded, '\n'), 0600); err != nil {
		return errors.New("seed committed but manifest could not be written")
	}
	logger.Info("seed completed", "profile", report.Profile, "counts", report.Counts, "database_bytes", report.DatabaseBytes, "manifest", manifest)
	return nil
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envFile(t *testing.T, settings string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("SITETRACK_DATABASE_URL=postgres://user:password@localhost/sitetrack\n"+settings), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSeparatePoolLimitsAndEnvironmentOverride(t *testing.T) {
	path := envFile(t, "SITETRACK_DATABASE_API_MAX_CONNECTIONS=15\nSITETRACK_DATABASE_WORKER_MAX_CONNECTIONS=10\nSITETRACK_DATABASE_MIGRATION_MAX_CONNECTIONS=1\nSITETRACK_RATE_LIMIT_ENABLED=false\n")
	t.Setenv("SITETRACK_DATABASE_API_MAX_CONNECTIONS", "23")
	cfg, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabasePools != (DatabasePools{API: 23, Worker: 10, Migration: 1}) {
		t.Fatalf("pool settings = %+v", cfg.DatabasePools)
	}
	if cfg.RateLimit.Enabled {
		t.Fatal("rate limiting could not be disabled for load testing")
	}
}

func TestRejectInvalidSettingsWithoutLeakingValues(t *testing.T) {
	for _, setting := range []string{
		"SITETRACK_DATABASE_API_MAX_CONNECTIONS=0",
		"SITETRACK_DATABASE_API_MAX_CONNECTIONS=1",
		"SITETRACK_DATABASE_WORKER_MAX_CONNECTIONS=-1",
		"SITETRACK_DATABASE_MIGRATION_MAX_CONNECTIONS=2147483648",
		"SITETRACK_RATE_LIMIT_ENABLED=secret-invalid-boolean",
		"SITETRACK_TRACE_SAMPLE_RATIO=NaN",
		"SITETRACK_TRACE_SAMPLE_RATIO=1.1",
		"SITETRACK_OTLP_ENDPOINT=https://user:secret@collector.example",
		"SITETRACK_OTLP_ENDPOINT=https://collector.example?key=secret",
		"SITETRACK_TELEMETRY_ENABLED=true\nSITETRACK_OTLP_API_KEY=",
	} {
		t.Run(strings.SplitN(setting, "=", 2)[0], func(t *testing.T) {
			_, err := load(envFile(t, setting+"\n"))
			if err == nil {
				t.Fatal("invalid settings were accepted")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("configuration error leaked a value: %s", err)
			}
		})
	}
}

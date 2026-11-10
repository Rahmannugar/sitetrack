package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strconv"
	"strings"

	"github.com/knadh/koanf/parsers/dotenv"
	koanfenv "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	HTTPPort       int
	DatabaseURL    string
	DatabasePools  DatabasePools
	RateLimit      RateLimit
	Telemetry      Telemetry
	Authentication Authentication
}

type DatabasePools struct {
	API       int32
	Worker    int32
	Migration int32
}

func Load() (Config, error) {
	return load(".env")
}

func load(path string) (Config, error) {
	k := koanf.New(".")
	const prefix = "SITETRACK_"
	transform := func(key, value string) (string, any) {
		return strings.ToLower(strings.TrimPrefix(key, prefix)), value
	}
	if err := k.Load(file.Provider(path), dotenv.ParserEnvWithValue(prefix, ".", transform)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, errors.New("could not load .env configuration")
	}
	if err := k.Load(koanfenv.Provider(".", koanfenv.Opt{Prefix: prefix, TransformFunc: transform}), nil); err != nil {
		return Config{}, errors.New("could not load environment configuration")
	}

	cfg := Config{
		HTTPPort: 8080, DatabaseURL: k.String("database_url"),
		DatabasePools: DatabasePools{API: 15, Worker: 10, Migration: 1},
	}
	if k.Exists("http_port") {
		port, err := strconv.Atoi(k.String("http_port"))
		if err != nil || port < 1 || port > 65535 {
			return Config{}, errors.New("SITETRACK_HTTP_PORT must be between 1 and 65535")
		}
		cfg.HTTPPort = port
	}
	for _, pool := range []struct {
		key   string
		limit *int32
	}{
		{"database_api_max_connections", &cfg.DatabasePools.API},
		{"database_worker_max_connections", &cfg.DatabasePools.Worker},
		{"database_migration_max_connections", &cfg.DatabasePools.Migration},
	} {
		if k.Exists(pool.key) {
			count, err := strconv.ParseInt(k.String(pool.key), 10, 32)
			if err != nil || count < 1 {
				return Config{}, fmt.Errorf("SITETRACK_%s must be a positive 32-bit integer", strings.ToUpper(pool.key))
			}
			*pool.limit = int32(count)
		}
	}
	databaseURL, err := url.Parse(cfg.DatabaseURL)
	if err != nil || databaseURL == nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") || databaseURL.Hostname() == "" || strings.Trim(databaseURL.Path, "/") == "" {
		return Config{}, errors.New("SITETRACK_DATABASE_URL must be a PostgreSQL URL with a host and database name")
	}
	cfg.Telemetry, err = loadTelemetry(k)
	if err != nil {
		return Config{}, err
	}
	cfg.RateLimit, err = loadRateLimit(k)
	if err != nil {
		return Config{}, err
	}
	cfg.Authentication, err = loadAuthentication(k)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) HTTPAddress() string {
	return fmt.Sprintf(":%d", cfg.HTTPPort)
}

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
	HTTPPort               int
	DatabaseURL            string
	DatabaseMaxConnections int32
}

func Load() (Config, error) {
	return load(".env")
}

func load(path string) (Config, error) {
	k := koanf.New(".")
	const prefix = "SITETRACK_"
	// Koanf requires an interface value at its provider boundary; configuration
	// is parsed into concrete types and validated before it reaches the runtime.
	transform := func(key, value string) (string, any) {
		return strings.ToLower(strings.TrimPrefix(key, prefix)), value
	}
	if err := k.Load(file.Provider(path), dotenv.ParserEnvWithValue(prefix, ".", transform)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, errors.New("could not load .env configuration")
	}
	if err := k.Load(koanfenv.Provider(".", koanfenv.Opt{Prefix: prefix, TransformFunc: transform}), nil); err != nil {
		return Config{}, errors.New("could not load environment configuration")
	}

	cfg := Config{HTTPPort: 8080, DatabaseMaxConnections: 10, DatabaseURL: k.String("database_url")}
	if k.Exists("http_port") {
		port, err := strconv.Atoi(k.String("http_port"))
		if err != nil || port < 1 || port > 65535 {
			return Config{}, errors.New("SITETRACK_HTTP_PORT must be between 1 and 65535")
		}
		cfg.HTTPPort = port
	}
	if k.Exists("database_max_connections") {
		count, err := strconv.ParseInt(k.String("database_max_connections"), 10, 32)
		if err != nil || count < 1 || count > 100 {
			return Config{}, errors.New("SITETRACK_DATABASE_MAX_CONNECTIONS must be between 1 and 100")
		}
		cfg.DatabaseMaxConnections = int32(count)
	}
	databaseURL, err := url.Parse(cfg.DatabaseURL)
	if err != nil || databaseURL == nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") || databaseURL.Hostname() == "" || strings.Trim(databaseURL.Path, "/") == "" {
		return Config{}, errors.New("SITETRACK_DATABASE_URL must be a PostgreSQL URL with a host and database name")
	}
	return cfg, nil
}

func (cfg Config) HTTPAddress() string {
	return fmt.Sprintf(":%d", cfg.HTTPPort)
}

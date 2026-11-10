package config

import (
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/knadh/koanf/v2"
)

type Telemetry struct {
	Enabled     bool
	Environment string
	Endpoint    string
	APIKey      string
	SampleRatio float64
}

func loadTelemetry(k *koanf.Koanf) (Telemetry, error) {
	cfg := Telemetry{Environment: "development", Endpoint: "https://otlp.nr-data.net", SampleRatio: 0.1}
	if k.Exists("telemetry_enabled") {
		enabled, err := strconv.ParseBool(k.String("telemetry_enabled"))
		if err != nil {
			return Telemetry{}, errors.New("SITETRACK_TELEMETRY_ENABLED must be a boolean")
		}
		cfg.Enabled = enabled
	}
	if k.Exists("environment") {
		cfg.Environment = k.String("environment")
	}
	switch cfg.Environment {
	case "development", "test", "staging", "production":
	default:
		return Telemetry{}, errors.New("SITETRACK_ENVIRONMENT must be development, test, staging or production")
	}
	if k.Exists("trace_sample_ratio") {
		ratio, err := strconv.ParseFloat(k.String("trace_sample_ratio"), 64)
		if err != nil || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
			return Telemetry{}, errors.New("SITETRACK_TRACE_SAMPLE_RATIO must be between 0 and 1")
		}
		cfg.SampleRatio = ratio
	}
	if k.Exists("otlp_endpoint") {
		cfg.Endpoint = k.String("otlp_endpoint")
	}
	cfg.APIKey = k.String("otlp_api_key")
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint == nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return Telemetry{}, errors.New("SITETRACK_OTLP_ENDPOINT must be an HTTPS origin without credentials, a query or a path")
	}
	if cfg.Enabled && strings.TrimSpace(cfg.APIKey) == "" {
		return Telemetry{}, errors.New("SITETRACK_OTLP_API_KEY is required when telemetry export is enabled")
	}
	if strings.ContainsAny(cfg.APIKey, "\r\n") {
		return Telemetry{}, errors.New("SITETRACK_OTLP_API_KEY must not contain line breaks")
	}
	return cfg, nil
}

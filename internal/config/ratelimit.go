package config

import (
	"errors"
	"strconv"

	"github.com/knadh/koanf/v2"
)

type RateLimit struct {
	Enabled bool
}

func loadRateLimit(k *koanf.Koanf) (RateLimit, error) {
	cfg := RateLimit{Enabled: true}
	if k.Exists("rate_limit_enabled") {
		enabled, err := strconv.ParseBool(k.String("rate_limit_enabled"))
		if err != nil {
			return RateLimit{}, errors.New("SITETRACK_RATE_LIMIT_ENABLED must be a boolean")
		}
		cfg.Enabled = enabled
	}
	return cfg, nil
}

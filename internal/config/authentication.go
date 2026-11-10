package config

import (
	"encoding/hex"
	"errors"
	"net/mail"

	"github.com/knadh/koanf/v2"
)

type Authentication struct {
	OTPSecret []byte
	OutboxKey []byte
	EmailFrom string
	ResendKey string
}

func loadAuthentication(k *koanf.Koanf) (Authentication, error) {
	cfg := Authentication{EmailFrom: "SiteTrack <onboarding@resend.dev>", ResendKey: k.String("resend_api_key")}
	if k.Exists("email_from") {
		cfg.EmailFrom = k.String("email_from")
	}
	if _, err := mail.ParseAddress(cfg.EmailFrom); err != nil {
		return Authentication{}, errors.New("SITETRACK_EMAIL_FROM must be an email address")
	}
	for _, setting := range []struct {
		key    string
		target *[]byte
	}{
		{"otp_secret", &cfg.OTPSecret},
		{"outbox_encryption_key", &cfg.OutboxKey},
	} {
		if raw := k.String(setting.key); raw != "" {
			value, err := hex.DecodeString(raw)
			if err != nil || len(value) != 32 {
				return Authentication{}, errors.New("authentication secrets must be 64 hexadecimal characters")
			}
			*setting.target = value
		}
	}
	return cfg, nil
}

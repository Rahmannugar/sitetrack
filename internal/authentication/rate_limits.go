package authentication

import (
	"time"

	"sitetrack/internal/infra/ratelimit"
)

var (
	forgotPasswordEmailRateLimit     = ratelimit.Policy{RequestsPerSecond: 1, Burst: 2, RequestsPerWindow: 10, Window: time.Hour}
	loginEmailRateLimit              = ratelimit.Policy{RequestsPerSecond: 1, Burst: 5, RequestsPerWindow: 20, Window: 10 * time.Minute}
	refreshAccountRateLimit          = ratelimit.Policy{RequestsPerSecond: 5, Burst: 10, RequestsPerWindow: 60, Window: time.Minute}
	resendVerificationEmailRateLimit = ratelimit.Policy{RequestsPerSecond: 1, Burst: 2, RequestsPerWindow: 10, Window: time.Hour}
	resetPasswordEmailRateLimit      = ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 10, Window: time.Minute}
	signupEmailRateLimit             = ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 10, Window: time.Hour}
	verifyEmailAddressRateLimit      = ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 10, Window: time.Minute}

	forgotPasswordRateLimit     = ratelimit.Policy{RequestsPerSecond: 2, Burst: 10, RequestsPerWindow: 60, Window: time.Hour}
	loginRateLimit              = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 300, Window: 10 * time.Minute}
	logoutRateLimit             = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	profileRateLimit            = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
	refreshRateLimit            = ratelimit.Policy{RequestsPerSecond: 20, Burst: 60, RequestsPerWindow: 1200, Window: time.Minute}
	resendVerificationRateLimit = ratelimit.Policy{RequestsPerSecond: 2, Burst: 10, RequestsPerWindow: 60, Window: time.Hour}
	resetPasswordRateLimit      = ratelimit.Policy{RequestsPerSecond: 2, Burst: 10, RequestsPerWindow: 60, Window: time.Minute}
	signupRateLimit             = ratelimit.Policy{RequestsPerSecond: 2, Burst: 10, RequestsPerWindow: 100, Window: time.Hour}
	verifyEmailRateLimit        = ratelimit.Policy{RequestsPerSecond: 5, Burst: 20, RequestsPerWindow: 120, Window: time.Minute}
)

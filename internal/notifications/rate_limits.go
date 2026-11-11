package notifications

import (
	"time"

	"sitetrack/internal/infra/ratelimit"
)

var (
	listNotificationsRateLimit    = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	readAllNotificationsRateLimit = ratelimit.Policy{RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute}
	readNotificationRateLimit     = ratelimit.Policy{RequestsPerSecond: 20, Burst: 60, RequestsPerWindow: 1200, Window: time.Minute}
	streamNotificationsRateLimit  = ratelimit.Policy{RequestsPerSecond: 10, Burst: 100, RequestsPerWindow: 600, Window: time.Minute}
	unreadCountRateLimit          = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
)

package deliveries

import (
	"sitetrack/internal/infra/ratelimit"
	"time"
)

var (
	createDeliveryRateLimit        = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	getDeliveryRateLimit           = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
	listCompanyDeliveriesRateLimit = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	listProjectDeliveriesRateLimit = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	receiveDeliveryRateLimit       = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	rejectDeliveryRateLimit        = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	cancelDeliveryRateLimit        = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
)

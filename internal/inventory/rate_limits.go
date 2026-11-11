package inventory

import (
	"time"

	"sitetrack/internal/infra/ratelimit"
)

var (
	addStockRateLimit            = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	getCompanyStoreItemRateLimit = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
	getProjectStoreItemRateLimit = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
	getCompanyStoreRateLimit     = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	getProjectStoreRateLimit     = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	setReorderPointRateLimit     = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
)

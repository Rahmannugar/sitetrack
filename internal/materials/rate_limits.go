package materials

import (
	"time"

	"sitetrack/internal/infra/ratelimit"
)

var (
	createCategoryRateLimit = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	createMaterialRateLimit = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	getCategoryRateLimit    = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
	getMaterialRateLimit    = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
	listCategoriesRateLimit = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	listMaterialsRateLimit  = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	removeCategoryRateLimit = ratelimit.Policy{RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute}
	removeMaterialRateLimit = ratelimit.Policy{RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute}
	updateCategoryRateLimit = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	updateMaterialRateLimit = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
)

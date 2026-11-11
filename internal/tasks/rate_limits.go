package tasks

import (
	"time"

	"sitetrack/internal/infra/ratelimit"
)

var (
	addDependencyRateLimit     = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	createTaskRateLimit        = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	getTaskRateLimit           = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
	listDependenciesRateLimit  = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	listRelationshipsRateLimit = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	listTasksRateLimit         = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	removeDependencyRateLimit  = ratelimit.Policy{RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute}
	removeTaskRateLimit        = ratelimit.Policy{RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute}
	updateTaskRateLimit        = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
)

var taskActionRateLimits = map[string]ratelimit.Policy{
	"done":   {RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute},
	"reopen": {RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute},
	"start":  {RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute},
}

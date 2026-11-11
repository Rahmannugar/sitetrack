package projects

import (
	"time"

	"sitetrack/internal/infra/ratelimit"
)

var (
	changeMemberRateLimit        = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	createProjectRateLimit       = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	getProjectRateLimit          = ratelimit.Policy{RequestsPerSecond: 100, Burst: 200, RequestsPerWindow: 6000, Window: time.Minute}
	inviteUserRateLimit          = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	listInvitationsRateLimit     = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	listMembersRateLimit         = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	listProjectsRateLimit        = ratelimit.Policy{RequestsPerSecond: 50, Burst: 100, RequestsPerWindow: 3000, Window: time.Minute}
	removeMemberRateLimit        = ratelimit.Policy{RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute}
	removeProjectRateLimit       = ratelimit.Policy{RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute}
	respondToInvitationRateLimit = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	updateProjectRateLimit       = ratelimit.Policy{RequestsPerSecond: 10, Burst: 30, RequestsPerWindow: 600, Window: time.Minute}
	withdrawInvitationRateLimit  = ratelimit.Policy{RequestsPerSecond: 5, Burst: 15, RequestsPerWindow: 300, Window: time.Minute}
)

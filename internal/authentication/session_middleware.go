package authentication

import (
	"context"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"
	"sitetrack/internal/infra/ratelimit"
)

type principalKey struct{}

func CurrentPrincipal(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

func RequireSession(accounts Accounts, logger *slog.Logger) gin.HandlerFunc {
	return sessionMiddleware(accounts, logger, nil)
}

func RequireRateLimitedSession(accounts Accounts, logger *slog.Logger, policy ratelimit.Policy, enabled bool) (gin.HandlerFunc, error) {
	limit, err := ratelimit.New(policy)
	if err != nil {
		return nil, err
	}
	if !enabled {
		limit = nil
	}
	return sessionMiddleware(accounts, logger, limit), nil
}

func sessionMiddleware(accounts Accounts, logger *slog.Logger, limit *ratelimit.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			accountError(c, logger, ErrUnauthenticated)
			c.Abort()
			return
		}
		principal, err := accounts.Authenticate(c.Request.Context(), parts[1])
		if err != nil {
			accountError(c, logger, err)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), principalKey{}, principal))
		if limit != nil && ratelimit.Respond(c, limit.Check("account:"+principal.User.ID.String())) {
			return
		}
		c.Next()
	}
}

package authentication

import (
	"context"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"
)

type principalKey struct{}

func CurrentPrincipal(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

func RequireSession(accounts Accounts, logger *slog.Logger) gin.HandlerFunc {
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
		c.Next()
	}
}

package authentication

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/Rahmannugar/authlier/emailaddress"
	"github.com/gin-gonic/gin"

	"sitetrack/internal/infra/ratelimit"
)

func identityRateLimit(limit *ratelimit.Limiter, enabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(ratelimit.WithIdentityLimiter(c.Request.Context(), limit, enabled))
		c.Next()
	}
}

func allowEmailRequest(c *gin.Context, email string) bool {
	normalized, err := emailaddress.Normalize(email)
	if err != nil {
		return true
	}
	proof := sha256.Sum256([]byte(normalized))
	return !ratelimit.Respond(c, ratelimit.CheckIdentity(c.Request.Context(), "email:"+hex.EncodeToString(proof[:])))
}

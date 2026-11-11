package authentication

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"sitetrack/internal/infra/ratelimit"
)

type rateLimitAccounts struct {
	Accounts
	userID uuid.UUID
}

func (a *rateLimitAccounts) Authenticate(context.Context, string) (Principal, error) {
	return Principal{User: Profile{ID: a.userID}}, nil
}

func TestAccountRateLimitAcrossIPsAndTokens(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		policy := ratelimit.Policy{RequestsPerSecond: 100, Burst: 100, RequestsPerWindow: 1, Window: time.Hour}
		accounts := &rateLimitAccounts{userID: uuid.New()}
		guard, err := RequireRateLimitedSession(accounts, slog.New(slog.NewTextHandler(io.Discard, nil)), policy, enabled)
		if err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		router.GET("/resource", guard, func(c *gin.Context) { c.Status(http.StatusNoContent) })
		call := func(ip, token string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, "/resource", nil)
			req.RemoteAddr = ip
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			return response
		}
		if got := call("192.0.2.1:1000", "first"); got.Code != 204 {
			t.Fatal(got.Code)
		}
		want := 204
		if enabled {
			want = 429
		}
		if got := call("192.0.2.2:1000", "replacement"); got.Code != want || (enabled && got.Header().Get("Retry-After") == "") {
			t.Fatalf("enabled=%v: %d", enabled, got.Code)
		}
		accounts.userID = uuid.New()
		if got := call("192.0.2.1:1000", "other"); got.Code != 204 {
			t.Fatal(got.Code)
		}
	}
}

func TestEmailRateLimitNormalizesIdentity(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		limit, err := ratelimit.New(ratelimit.Policy{RequestsPerSecond: 100, Burst: 100, RequestsPerWindow: 1, Window: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		router.POST("/email", identityRateLimit(limit, enabled), func(c *gin.Context) {
			if allowEmailRequest(c, c.Query("email")) {
				c.Status(http.StatusNoContent)
			}
		})
		for i, email := range []string{"User@example.test", "user@example.test", "other@example.test"} {
			req := httptest.NewRequest(http.MethodPost, "/email?email="+email, nil)
			req.RemoteAddr = []string{"192.0.2.1:1000", "192.0.2.2:1000", "192.0.2.1:1000"}[i]
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			want := 204
			if enabled && i == 1 {
				want = 429
			}
			if response.Code != want {
				t.Fatalf("enabled=%v email=%s: %d", enabled, email, response.Code)
			}
		}
	}
}

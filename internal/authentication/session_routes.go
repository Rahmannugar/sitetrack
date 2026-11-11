package authentication

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/ratelimit"
	"sitetrack/internal/infra/request"
)

func registerSessions(group *gin.RouterGroup, accounts Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	login, err := request.NewBody[LoginInput]()
	if err != nil {
		return err
	}
	refresh, err := request.NewBody[RefreshInput]()
	if err != nil {
		return err
	}
	group = group.Group("", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	for _, endpoint := range []struct {
		method, path   string
		policy         ratelimit.Policy
		identityPolicy ratelimit.Policy
		operation      openapi.Operation
		protected      bool
		handler        gin.HandlerFunc
	}{
		{http.MethodPost, "/login", loginRateLimit, loginEmailRateLimit, loginContract(login.Schema), false,
			func(c *gin.Context) {
				input, err := login.Decode(c)
				if err != nil {
					accountError(c, logger, request.ErrInvalid)
					return
				}
				if !allowEmailRequest(c, input.Email) {
					return
				}
				pair, err := accounts.Login(c.Request.Context(), input)
				if err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusOK, pair)
			}},
		{http.MethodPost, "/refresh", refreshRateLimit, refreshAccountRateLimit, refreshContract(refresh.Schema), false,
			func(c *gin.Context) {
				input, err := refresh.Decode(c)
				if err != nil {
					accountError(c, logger, request.ErrInvalid)
					return
				}
				pair, err := accounts.Refresh(c.Request.Context(), input.RefreshToken)
				if err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusOK, pair)
			}},
		{http.MethodGet, "/me", profileRateLimit, profileRateLimit, profileContract(), true,
			func(c *gin.Context) {
				principal, _ := CurrentPrincipal(c.Request.Context())
				c.JSON(http.StatusOK, principal.User)
			}},
		{http.MethodPost, "/logout", logoutRateLimit, logoutRateLimit, logoutContract(), true,
			func(c *gin.Context) {
				principal, _ := CurrentPrincipal(c.Request.Context())
				if err := accounts.Logout(c.Request.Context(), principal); err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusOK, messageResponse{Message: "Logged out."})
			}},
	} {
		limit, err := ratelimit.New(endpoint.policy)
		if err != nil {
			return err
		}
		handlers := []gin.HandlerFunc{limit.Middleware(enabled)}
		if endpoint.protected {
			session, err := RequireRateLimitedSession(accounts, logger, endpoint.identityPolicy, enabled)
			if err != nil {
				return err
			}
			handlers = append(handlers, session)
		} else {
			identityLimit, err := ratelimit.New(endpoint.identityPolicy)
			if err != nil {
				return err
			}
			handlers = append(handlers, identityRateLimit(identityLimit, enabled))
		}
		handlers = append(handlers, endpoint.handler)
		if err := contracts.Register(group, endpoint.method, endpoint.path, endpoint.operation, handlers...); err != nil {
			return err
		}
	}
	return nil
}

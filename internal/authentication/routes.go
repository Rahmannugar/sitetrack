package authentication

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/ratelimit"
	"sitetrack/internal/infra/request"
)

type messageResponse struct {
	Message string `json:"message"`
}
type errorResponse struct {
	Error string `json:"error"`
}

func Register(router *gin.RouterGroup, accounts Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	signup, err := request.NewBody[SignupInput]()
	if err != nil {
		return err
	}
	verify, err := request.NewBody[VerificationInput]()
	if err != nil {
		return err
	}
	resend, err := request.NewBody[ResendInput]()
	if err != nil {
		return err
	}
	group := router.Group("/auth")
	for _, endpoint := range []struct {
		path      string
		policy    ratelimit.Policy
		operation openapi.Operation
		handler   gin.HandlerFunc
	}{
		{"/signup", ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 10, Window: time.Hour},
			signupContract(signup.Schema), func(c *gin.Context) {
				input, err := signup.Decode(c)
				if err != nil {
					c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid request body"})
					return
				}
				if err := accounts.Signup(c.Request.Context(), input); err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusAccepted, messageResponse{Message: "Check your email for a verification code."})
			}},
		{"/verify-email", ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 30, Window: time.Minute},
			verifyContract(verify.Schema), func(c *gin.Context) {
				input, err := verify.Decode(c)
				if err != nil {
					c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid request body"})
					return
				}
				if err := accounts.Verify(c.Request.Context(), input); err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusOK, messageResponse{Message: "Email verified."})
			}},
		{"/resend-verification", ratelimit.Policy{RequestsPerSecond: 1, Burst: 2, RequestsPerWindow: 5, Window: time.Hour},
			resendContract(resend.Schema), func(c *gin.Context) {
				input, err := resend.Decode(c)
				if err != nil {
					c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid request body"})
					return
				}
				if err := accounts.Resend(c.Request.Context(), input.Email); err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusAccepted, messageResponse{Message: "Check your email for a verification code."})
			}},
	} {
		limit, err := ratelimit.New(endpoint.policy)
		if err != nil {
			return err
		}
		if err := contracts.Register(group, http.MethodPost, endpoint.path, endpoint.operation, limit.Middleware(enabled), endpoint.handler); err != nil {
			return err
		}
	}
	return registerSessions(group, accounts, contracts, enabled, logger)
}

func accountError(c *gin.Context, logger *slog.Logger, err error) {
	if errors.Is(err, ErrUnauthenticated) || errors.Is(err, ErrInvalidCredentials) {
		c.Header("WWW-Authenticate", "Bearer")
		c.JSON(http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}
	if errors.Is(err, request.ErrInvalid) {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrInvalidVerification) {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		c.JSON(http.StatusGatewayTimeout, errorResponse{Error: "request timed out"})
		return
	}
	logger.ErrorContext(c.Request.Context(), "account operation failed", "route", c.FullPath())
	c.JSON(http.StatusInternalServerError, errorResponse{Error: "internal server error"})
}

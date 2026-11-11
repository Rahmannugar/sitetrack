package authentication

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

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
	reset, err := request.NewBody[ResetPasswordInput]()
	if err != nil {
		return err
	}
	group := router.Group("/auth")
	for _, endpoint := range []struct {
		path        string
		policy      ratelimit.Policy
		emailPolicy ratelimit.Policy
		operation   openapi.Operation
		handler     gin.HandlerFunc
	}{
		{"/signup", signupRateLimit, signupEmailRateLimit,
			signupContract(signup.Schema), func(c *gin.Context) {
				input, err := signup.Decode(c)
				if err != nil {
					c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid request body"})
					return
				}
				if !allowEmailRequest(c, input.Email) {
					return
				}
				if err := accounts.Signup(c.Request.Context(), input); err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusCreated, messageResponse{Message: "Check your email for a verification code."})
			}},
		{"/verify-email", verifyEmailRateLimit, verifyEmailAddressRateLimit,
			verifyContract(verify.Schema), func(c *gin.Context) {
				input, err := verify.Decode(c)
				if err != nil {
					c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid request body"})
					return
				}
				if !allowEmailRequest(c, input.Email) {
					return
				}
				if err := accounts.Verify(c.Request.Context(), input); err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusOK, messageResponse{Message: "Email verified."})
			}},
		{"/forgot-password", forgotPasswordRateLimit, forgotPasswordEmailRateLimit,
			forgotPasswordContract(resend.Schema), func(c *gin.Context) {
				input, err := resend.Decode(c)
				if err != nil {
					accountError(c, logger, err)
					return
				}
				if !allowEmailRequest(c, input.Email) {
					return
				}
				if err := accounts.ForgotPassword(c.Request.Context(), input.Email); err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusAccepted, messageResponse{Message: "Check your email for a password reset code."})
			}},
		{"/reset-password", resetPasswordRateLimit, resetPasswordEmailRateLimit,
			resetPasswordContract(reset.Schema), func(c *gin.Context) {
				input, err := reset.Decode(c)
				if err != nil {
					accountError(c, logger, err)
					return
				}
				if !allowEmailRequest(c, input.Email) {
					return
				}
				if err := accounts.ResetPassword(c.Request.Context(), input); err != nil {
					accountError(c, logger, err)
					return
				}
				c.JSON(http.StatusOK, messageResponse{Message: "Password reset. Log in with your new password."})
			}},
		{"/resend-verification", resendVerificationRateLimit, resendVerificationEmailRateLimit,
			resendContract(resend.Schema), func(c *gin.Context) {
				input, err := resend.Decode(c)
				if err != nil {
					c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid request body"})
					return
				}
				if !allowEmailRequest(c, input.Email) {
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
		emailLimit, err := ratelimit.New(endpoint.emailPolicy)
		if err != nil {
			return err
		}
		if err := contracts.Register(group, http.MethodPost, endpoint.path, endpoint.operation, limit.Middleware(enabled), identityRateLimit(emailLimit, enabled), endpoint.handler); err != nil {
			return err
		}
	}
	return registerSessions(group, accounts, contracts, enabled, logger)
}

func accountError(c *gin.Context, logger *slog.Logger, err error) {
	if ratelimit.Respond(c, err) {
		return
	}
	if errors.Is(err, ErrEmailRegistered) {
		c.JSON(http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	if errors.Is(err, ErrEmailUnverified) {
		c.JSON(http.StatusForbidden, errorResponse{Error: err.Error()})
		return
	}
	if errors.Is(err, ErrUnauthenticated) || errors.Is(err, ErrInvalidCredentials) {
		c.Header("WWW-Authenticate", "Bearer")
		c.JSON(http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}
	if errors.Is(err, request.ErrInvalid) {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrInvalidVerification) || errors.Is(err, ErrInvalidReset) {
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

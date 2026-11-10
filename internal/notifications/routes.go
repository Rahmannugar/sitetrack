package notifications

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
	"sitetrack/internal/authentication"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/ratelimit"
	"sitetrack/internal/infra/request"
	"time"
)

type notificationPath struct {
	ID string `json:"id" uri:"id" binding:"required,uuid"`
}

func Register(router, streams *gin.RouterGroup, service *Service, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	list, err := request.NewContract[ListInput]()
	if err != nil {
		return err
	}
	path, err := request.NewContract[notificationPath]()
	if err != nil {
		return err
	}
	stream, err := request.NewContract[streamInput]()
	if err != nil {
		return err
	}
	group := router.Group("/notifications", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	for _, endpoint := range []struct {
		method, path string
		operation    openapi.Operation
		policy       ratelimit.Policy
		handler      gin.HandlerFunc
	}{
		{"GET", "", listContract(list.Schema), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 300, Window: time.Minute}, func(c *gin.Context) {
			input, err := list.DecodeQuery(c)
			if err != nil {
				notificationError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			page, err := service.List(c.Request.Context(), principal.User.ID, input)
			if err != nil {
				notificationError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, page)
		}},
		{"GET", "/unread-count", unreadContract(), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 600, Window: time.Minute}, func(c *gin.Context) {
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			count, err := service.Unread(c.Request.Context(), principal.User.ID)
			if err != nil {
				notificationError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, streamState{UnreadCount: count})
		}},
		{"PATCH", "/:id/read", readContract(path.Schema, false), ratelimit.Policy{RequestsPerSecond: 5, Burst: 10, RequestsPerWindow: 120, Window: time.Minute}, func(c *gin.Context) {
			input, err := path.DecodePath(c)
			if err != nil {
				notificationError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			if err = service.Read(c.Request.Context(), principal.User.ID, uuid.MustParse(input.ID)); err != nil {
				notificationError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
		{"PATCH", "/read-all", readContract(nil, true), ratelimit.Policy{RequestsPerSecond: 1, Burst: 2, RequestsPerWindow: 20, Window: time.Minute}, func(c *gin.Context) {
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			if err := service.ReadAll(c.Request.Context(), principal.User.ID); err != nil {
				notificationError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
	} {
		limiter, err := ratelimit.New(endpoint.policy)
		if err != nil {
			return err
		}
		if err = contracts.Register(group, endpoint.method, endpoint.path, endpoint.operation, limiter.Middleware(enabled), authentication.RequireSession(accounts, logger), endpoint.handler); err != nil {
			return err
		}
	}
	limiter, err := ratelimit.New(ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 20, Window: time.Minute})
	if err != nil {
		return err
	}
	return contracts.Register(streams, "GET", "/notifications/stream", streamContract(stream.Schema), limiter.Middleware(enabled), authentication.RequireSession(accounts, logger), func(c *gin.Context) {
		input, err := stream.DecodeHeader(c)
		if err != nil {
			notificationError(c, logger, ErrInvalid)
			return
		}
		service.Stream(c, accounts, input)
	})
}
func notificationError(c *gin.Context, logger *slog.Logger, err error) {
	status, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, request.ErrInvalid):
		status, message = http.StatusBadRequest, ErrInvalid.Error()
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "request timed out"
	default:
		logger.ErrorContext(c.Request.Context(), "notification operation failed", "route", c.FullPath())
	}
	c.JSON(status, struct {
		Error string `json:"error"`
	}{message})
}

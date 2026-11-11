package projects

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"sitetrack/internal/authentication"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/ratelimit"
	"sitetrack/internal/infra/request"
)

type projectPath struct {
	ID string `json:"id" uri:"id" binding:"required,max=32"`
}
type memberPath struct {
	ID     string `json:"id" uri:"id" binding:"required,max=32"`
	UserID string `json:"user_id" uri:"user_id" binding:"required,uuid"`
}

func Register(router *gin.RouterGroup, service *Service, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	input, err := request.NewBody[Input]()
	if err != nil {
		return err
	}
	member, err := request.NewBody[MemberInput]()
	if err != nil {
		return err
	}
	listing, err := request.NewContract[ListInput]()
	if err != nil {
		return err
	}
	members, err := request.NewContract[MemberListInput]()
	if err != nil {
		return err
	}
	projectParams, err := request.NewContract[projectPath]()
	if err != nil {
		return err
	}
	memberParams, err := request.NewContract[memberPath]()
	if err != nil {
		return err
	}
	group := router.Group("/projects", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	for _, endpoint := range []struct {
		method, path string
		operation    openapi.Operation
		policy       ratelimit.Policy
		handler      gin.HandlerFunc
	}{
		{http.MethodPost, "", createContract(input.Schema), ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 30, Window: time.Minute}, func(c *gin.Context) {
			value, err := input.Decode(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Create(c.Request.Context(), principal.User.ID, value)
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.Header("Location", "/projects/"+result.ID)
			c.JSON(http.StatusCreated, result)
		}},
		{http.MethodGet, "", listContract(listing.Schema), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 300, Window: time.Minute}, func(c *gin.Context) {
			value, err := listing.DecodeQuery(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.List(c.Request.Context(), principal.User.ID, value)
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodGet, "/:id", readContract(projectParams.Schema), ratelimit.Policy{RequestsPerSecond: 20, Burst: 40, RequestsPerWindow: 600, Window: time.Minute}, func(c *gin.Context) {
			path, err := projectParams.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Get(c.Request.Context(), principal.User.ID, path.ID)
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodPut, "/:id", updateContract(input.Schema, projectParams.Schema), ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 60, Window: time.Minute}, func(c *gin.Context) {
			path, err := projectParams.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			value, err := input.Decode(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Update(c.Request.Context(), principal.User.ID, path.ID, value)
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodDelete, "/:id", removeContract(projectParams.Schema), ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 20, Window: time.Minute}, func(c *gin.Context) {
			path, err := projectParams.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			if err := service.Remove(c.Request.Context(), principal.User.ID, path.ID); err != nil {
				projectError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
		{http.MethodGet, "/:id/members", membersContract(members.Schema, projectParams.Schema), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 300, Window: time.Minute}, func(c *gin.Context) {
			path, err := projectParams.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			value, err := members.DecodeQuery(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Members(c.Request.Context(), principal.User.ID, path.ID, value)
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodPatch, "/:id/members/:user_id", changeMemberContract(member.Schema, memberParams.Schema), ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 60, Window: time.Minute}, func(c *gin.Context) {
			path, err := memberParams.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			value, err := member.Decode(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			if err := service.ChangeMember(c.Request.Context(), principal.User.ID, path.ID, uuid.MustParse(path.UserID), value.IsAdmin); err != nil {
				projectError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
		{http.MethodDelete, "/:id/members/:user_id", removeMemberContract(memberParams.Schema), ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 60, Window: time.Minute}, func(c *gin.Context) {
			path, err := memberParams.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			if err := service.ChangeMember(c.Request.Context(), principal.User.ID, path.ID, uuid.MustParse(path.UserID), nil); err != nil {
				projectError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
	} {
		limiter, err := ratelimit.New(endpoint.policy)
		if err != nil {
			return err
		}
		if err := contracts.Register(group, endpoint.method, endpoint.path, endpoint.operation, limiter.Middleware(enabled), authentication.RequireSession(accounts, logger), endpoint.handler); err != nil {
			return err
		}
	}
	return registerInvitations(router, service, accounts, contracts, enabled, logger)
}
func projectError(c *gin.Context, logger *slog.Logger, err error) {
	code, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, request.ErrInvalid):
		code, message = http.StatusBadRequest, ErrInvalid.Error()
	case errors.Is(err, ErrInvitationNotFound):
		code, message = http.StatusNotFound, ErrInvitationNotFound.Error()
	case errors.Is(err, ErrInvitationConflict):
		code, message = http.StatusConflict, ErrInvitationConflict.Error()
	case errors.Is(err, ErrNotFound):
		code, message = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, ErrForbidden):
		code, message = http.StatusForbidden, ErrForbidden.Error()
	case errors.Is(err, ErrStock):
		code, message = http.StatusConflict, ErrStock.Error()
	case errors.Is(err, ErrLastAdmin):
		code, message = http.StatusConflict, ErrLastAdmin.Error()
	case errors.Is(err, context.DeadlineExceeded):
		code, message = http.StatusGatewayTimeout, "request timed out"
	default:
		logger.ErrorContext(c.Request.Context(), "project operation failed", "route", c.FullPath())
	}
	c.JSON(code, struct {
		Error string `json:"error"`
	}{message})
}

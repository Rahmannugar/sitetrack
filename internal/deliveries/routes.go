package deliveries

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
)

type deliveryPath struct {
	DeliveryID string `json:"delivery_id" uri:"delivery_id" binding:"required,uuid"`
}
type projectPath struct {
	ID string `json:"id" uri:"id" binding:"required,max=32"`
}

func actor(c *gin.Context) uuid.UUID {
	p, _ := authentication.CurrentPrincipal(c.Request.Context())
	return p.User.ID
}
func Register(router *gin.RouterGroup, service *Service, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	body, err := request.NewBody[Input]()
	if err != nil {
		return err
	}
	list, err := request.NewContract[ListInput]()
	if err != nil {
		return err
	}
	path, err := request.NewContract[deliveryPath]()
	if err != nil {
		return err
	}
	project, err := request.NewContract[projectPath]()
	if err != nil {
		return err
	}
	type endpoint struct {
		method, path string
		policy       ratelimit.Policy
		operation    openapi.Operation
		handler      gin.HandlerFunc
	}
	endpoints := []endpoint{
		{"POST", "/deliveries", createDeliveryRateLimit, createContract(body.Schema), func(c *gin.Context) {
			value, err := body.Decode(c)
			if err != nil {
				deliveryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.Create(c.Request.Context(), actor(c), value)
			if err != nil {
				deliveryError(c, logger, err)
				return
			}
			c.JSON(http.StatusCreated, result)
		}},
		{"GET", "/deliveries/:delivery_id", getDeliveryRateLimit, detailContract(path.Schema), func(c *gin.Context) {
			value, err := path.DecodePath(c)
			if err != nil {
				deliveryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.Get(c.Request.Context(), actor(c), uuid.MustParse(value.DeliveryID))
			if err != nil {
				deliveryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"GET", "/company-store/deliveries", listCompanyDeliveriesRateLimit, listContract(nil, list.Schema, true), func(c *gin.Context) {
			value, err := list.DecodeQuery(c)
			if err != nil {
				deliveryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.List(c.Request.Context(), actor(c), "", value)
			if err != nil {
				deliveryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"GET", "/projects/:id/deliveries", listProjectDeliveriesRateLimit, listContract(project.Schema, list.Schema, false), func(c *gin.Context) {
			scope, err := project.DecodePath(c)
			if err != nil {
				deliveryError(c, logger, ErrInvalid)
				return
			}
			value, err := list.DecodeQuery(c)
			if err != nil {
				deliveryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.List(c.Request.Context(), actor(c), scope.ID, value)
			if err != nil {
				deliveryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
	}
	for _, action := range []struct {
		name   string
		policy ratelimit.Policy
	}{{"cancel", cancelDeliveryRateLimit}, {"receive", receiveDeliveryRateLimit}, {"reject", rejectDeliveryRateLimit}} {
		endpoints = append(endpoints, endpoint{"POST", "/deliveries/:delivery_id/" + action.name, action.policy, transitionContract(action.name, path.Schema), func(c *gin.Context) {
			value, err := path.DecodePath(c)
			if err != nil {
				deliveryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.Transition(c.Request.Context(), actor(c), uuid.MustParse(value.DeliveryID), action.name)
			if err != nil {
				deliveryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}})
	}
	for _, endpoint := range endpoints {
		limit, err := ratelimit.New(endpoint.policy)
		if err != nil {
			return err
		}
		session, err := authentication.RequireRateLimitedSession(accounts, logger, endpoint.policy, enabled)
		if err != nil {
			return err
		}
		if err := contracts.Register(router, endpoint.method, endpoint.path, endpoint.operation, func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() }, limit.Middleware(enabled), session, endpoint.handler); err != nil {
			return err
		}
	}
	return nil
}
func deliveryError(c *gin.Context, logger *slog.Logger, err error) {
	status, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, ErrInvalid):
		status, message = http.StatusBadRequest, ErrInvalid.Error()
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, ErrForbidden):
		status, message = http.StatusForbidden, ErrForbidden.Error()
	case errors.Is(err, ErrState), errors.Is(err, ErrStock), errors.Is(err, ErrCapacity), errors.Is(err, ErrMaterial):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "request timed out"
	default:
		logger.ErrorContext(c.Request.Context(), "delivery operation failed", "route", c.FullPath())
	}
	c.JSON(status, struct {
		Error string `json:"error"`
	}{message})
}

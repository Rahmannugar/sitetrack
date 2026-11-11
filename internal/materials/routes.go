package materials

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

type resourcePath struct {
	ID string `json:"id" uri:"id" binding:"required,uuid"`
}
type endpoint struct {
	method, path string
	operation    openapi.Operation
	policy       ratelimit.Policy
	handler      gin.HandlerFunc
}

func Register(router *gin.RouterGroup, service *Service, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	input, err := request.NewBody[Input]()
	if err != nil {
		return err
	}
	listing, err := request.NewContract[ListInput]()
	if err != nil {
		return err
	}
	path, err := request.NewContract[resourcePath]()
	if err != nil {
		return err
	}
	endpoints := []endpoint{
		{http.MethodPost, "", resourceContract("createMaterial", "Create a material", input.Schema, nil, false, true), ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 60, Window: time.Minute}, func(c *gin.Context) {
			value, err := input.Decode(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			result, err := service.Create(c.Request.Context(), actor(c), value)
			if err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.Header("Location", "/materials/"+result.ID.String())
			c.JSON(http.StatusCreated, result)
		}},
		{http.MethodGet, "", listingContract("listMaterials", "List materials", listing.Schema, false), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 300, Window: time.Minute}, func(c *gin.Context) {
			value, err := listing.DecodeQuery(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			result, err := service.List(c.Request.Context(), actor(c), value)
			if err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodGet, "/:id", resourceContract("getMaterial", "View a material", nil, path.Schema, false, false), ratelimit.Policy{RequestsPerSecond: 20, Burst: 40, RequestsPerWindow: 600, Window: time.Minute}, func(c *gin.Context) {
			value, err := path.DecodePath(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			result, err := service.Get(c.Request.Context(), actor(c), uuid.MustParse(value.ID))
			if err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodPut, "/:id", resourceContract("updateMaterial", "Update a material", input.Schema, path.Schema, false, false), ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 90, Window: time.Minute}, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			value, err := input.Decode(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			result, err := service.Update(c.Request.Context(), actor(c), uuid.MustParse(params.ID), value)
			if err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodDelete, "/:id", removalContract("removeMaterial", "Remove a material", path.Schema, false), ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 30, Window: time.Minute}, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			if err := service.Remove(c.Request.Context(), actor(c), uuid.MustParse(params.ID)); err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
	}
	if err := registerEndpoints(router.Group("/materials"), endpoints, accounts, contracts, enabled, logger); err != nil {
		return err
	}
	return registerCategories(router, service, accounts, contracts, enabled, logger)
}

func registerCategories(router *gin.RouterGroup, service *Service, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	input, err := request.NewBody[CategoryInput]()
	if err != nil {
		return err
	}
	listing, err := request.NewContract[CategoryListInput]()
	if err != nil {
		return err
	}
	path, err := request.NewContract[resourcePath]()
	if err != nil {
		return err
	}
	return registerEndpoints(router.Group("/material-categories"), []endpoint{
		{http.MethodPost, "", resourceContract("createMaterialCategory", "Create a material category", input.Schema, nil, true, true), ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 30, Window: time.Minute}, func(c *gin.Context) {
			value, err := input.Decode(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			result, err := service.CreateCategory(c.Request.Context(), actor(c), value)
			if err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.Header("Location", "/material-categories/"+result.ID.String())
			c.JSON(http.StatusCreated, result)
		}},
		{http.MethodGet, "", listingContract("listMaterialCategories", "List material categories", listing.Schema, true), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 300, Window: time.Minute}, func(c *gin.Context) {
			value, err := listing.DecodeQuery(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			result, err := service.Categories(c.Request.Context(), actor(c), value)
			if err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodGet, "/:id", resourceContract("getMaterialCategory", "View a material category", nil, path.Schema, true, false), ratelimit.Policy{RequestsPerSecond: 20, Burst: 40, RequestsPerWindow: 600, Window: time.Minute}, func(c *gin.Context) {
			value, err := path.DecodePath(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			result, err := service.GetCategory(c.Request.Context(), actor(c), uuid.MustParse(value.ID))
			if err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodPut, "/:id", resourceContract("updateMaterialCategory", "Update a material category", input.Schema, path.Schema, true, false), ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 30, Window: time.Minute}, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			value, err := input.Decode(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			result, err := service.UpdateCategory(c.Request.Context(), actor(c), uuid.MustParse(params.ID), value)
			if err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{http.MethodDelete, "/:id", removalContract("removeMaterialCategory", "Remove a material category", path.Schema, true), ratelimit.Policy{RequestsPerSecond: 1, Burst: 2, RequestsPerWindow: 20, Window: time.Minute}, func(c *gin.Context) {
			value, err := path.DecodePath(c)
			if err != nil {
				catalogueError(c, logger, ErrInvalid)
				return
			}
			if err := service.RemoveCategory(c.Request.Context(), actor(c), uuid.MustParse(value.ID)); err != nil {
				catalogueError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
	}, accounts, contracts, enabled, logger)
}
func actor(c *gin.Context) uuid.UUID {
	principal, _ := authentication.CurrentPrincipal(c.Request.Context())
	return principal.User.ID
}
func registerEndpoints(group *gin.RouterGroup, endpoints []endpoint, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	group.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	for _, endpoint := range endpoints {
		limit, err := ratelimit.New(endpoint.policy)
		if err != nil {
			return err
		}
		if err := contracts.Register(group, endpoint.method, endpoint.path, endpoint.operation, limit.Middleware(enabled), authentication.RequireSession(accounts, logger), endpoint.handler); err != nil {
			return err
		}
	}
	return nil
}
func catalogueError(c *gin.Context, logger *slog.Logger, err error) {
	status, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, request.ErrInvalid):
		status, message = http.StatusBadRequest, ErrInvalid.Error()
	case errors.Is(err, ErrForbidden):
		status, message = http.StatusForbidden, ErrForbidden.Error()
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, ErrConflict):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "request timed out"
	default:
		logger.ErrorContext(c.Request.Context(), "catalogue operation failed", "route", c.FullPath())
	}
	c.JSON(status, struct {
		Error string `json:"error"`
	}{message})
}

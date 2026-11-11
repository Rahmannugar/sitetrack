package inventory

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

type projectPath struct {
	ID string `json:"id" uri:"id" binding:"required,max=32"`
}
type itemPath struct {
	MaterialID string `json:"material_id" uri:"material_id" binding:"required,uuid"`
}
type projectItemPath struct {
	ID         string `json:"id" uri:"id" binding:"required,max=32"`
	MaterialID string `json:"material_id" uri:"material_id" binding:"required,uuid"`
}
type endpoint struct {
	method, path string
	operation    openapi.Operation
	policy       ratelimit.Policy
	handler      gin.HandlerFunc
}

func actor(c *gin.Context) uuid.UUID {
	principal, _ := authentication.CurrentPrincipal(c.Request.Context())
	return principal.User.ID
}

func Register(router *gin.RouterGroup, service *Service, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	amount, err := request.NewBody[QuantityInput]()
	if err != nil {
		return err
	}
	reorder, err := request.NewBody[ReorderInput]()
	if err != nil {
		return err
	}
	companyList, err := request.NewContract[CompanyListInput]()
	if err != nil {
		return err
	}
	projectList, err := request.NewContract[ProjectListInput]()
	if err != nil {
		return err
	}
	item, err := request.NewContract[itemPath]()
	if err != nil {
		return err
	}
	project, err := request.NewContract[projectPath]()
	if err != nil {
		return err
	}
	projectItem, err := request.NewContract[projectItemPath]()
	if err != nil {
		return err
	}
	if err := registerEndpoints(router.Group("/company-store"), []endpoint{
		{"GET", "", storeContract(nil, true), ratelimit.Policy{RequestsPerSecond: 20, Burst: 40, RequestsPerWindow: 600, Window: time.Minute}, func(c *gin.Context) {
			result, err := service.CompanyStore(c.Request.Context(), actor(c))
			if err != nil {
				inventoryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"GET", "/inventory", listContract(nil, companyList.Schema, true), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 300, Window: time.Minute}, func(c *gin.Context) {
			value, err := companyList.DecodeQuery(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.CompanyStock(c.Request.Context(), actor(c), value)
			if err != nil {
				inventoryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"GET", "/inventory/:material_id", itemContract("getCompanyInventoryItem", "View company stock for a material", nil, item.Schema, true), ratelimit.Policy{RequestsPerSecond: 20, Burst: 40, RequestsPerWindow: 600, Window: time.Minute}, func(c *gin.Context) {
			path, err := item.DecodePath(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.CompanyItem(c.Request.Context(), actor(c), uuid.MustParse(path.MaterialID))
			if err != nil {
				inventoryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"POST", "/inventory/:material_id/add-stock", itemContract("addCompanyStock", "Add supplier stock to the company store", amount.Schema, item.Schema, true), ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 60, Window: time.Minute}, func(c *gin.Context) {
			path, err := item.DecodePath(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			value, err := amount.Decode(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.AddStock(c.Request.Context(), actor(c), uuid.MustParse(path.MaterialID), value)
			if err != nil {
				inventoryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"PUT", "/inventory/:material_id/reorder-point", itemContract("setCompanyReorderPoint", "Set a material’s company reorder point", reorder.Schema, item.Schema, true), ratelimit.Policy{RequestsPerSecond: 1, Burst: 3, RequestsPerWindow: 30, Window: time.Minute}, func(c *gin.Context) {
			path, err := item.DecodePath(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			value, err := reorder.Decode(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.SetReorderPoint(c.Request.Context(), actor(c), uuid.MustParse(path.MaterialID), value)
			if err != nil {
				inventoryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
	}, accounts, contracts, enabled, logger); err != nil {
		return err
	}
	return registerEndpoints(router.Group("/projects/:id/store"), []endpoint{
		{"GET", "", storeContract(project.Schema, false), ratelimit.Policy{RequestsPerSecond: 20, Burst: 40, RequestsPerWindow: 600, Window: time.Minute}, func(c *gin.Context) {
			path, err := project.DecodePath(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.ProjectStore(c.Request.Context(), actor(c), path.ID)
			if err != nil {
				inventoryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"GET", "/inventory", listContract(project.Schema, projectList.Schema, false), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 300, Window: time.Minute}, func(c *gin.Context) {
			path, err := project.DecodePath(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			value, err := projectList.DecodeQuery(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.ProjectStock(c.Request.Context(), actor(c), path.ID, value)
			if err != nil {
				inventoryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"GET", "/inventory/:material_id", itemContract("getProjectInventoryItem", "View project stock for a material", nil, projectItem.Schema, false), ratelimit.Policy{RequestsPerSecond: 20, Burst: 40, RequestsPerWindow: 600, Window: time.Minute}, func(c *gin.Context) {
			path, err := projectItem.DecodePath(c)
			if err != nil {
				inventoryError(c, logger, ErrInvalid)
				return
			}
			result, err := service.ProjectItem(c.Request.Context(), actor(c), path.ID, uuid.MustParse(path.MaterialID))
			if err != nil {
				inventoryError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
	}, accounts, contracts, enabled, logger)
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
func inventoryError(c *gin.Context, logger *slog.Logger, err error) {
	status, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, request.ErrInvalid):
		status, message = http.StatusBadRequest, ErrInvalid.Error()
	case errors.Is(err, ErrForbidden):
		status, message = http.StatusForbidden, ErrForbidden.Error()
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, ErrConflict):
		status, message = http.StatusConflict, ErrConflict.Error()
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "request timed out"
	default:
		logger.ErrorContext(c.Request.Context(), "inventory operation failed", "route", c.FullPath())
	}
	c.JSON(status, struct {
		Error string `json:"error"`
	}{message})
}

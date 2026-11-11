package tasks

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
	"sitetrack/internal/authentication"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/ratelimit"
	"sitetrack/internal/infra/request"
)

type projectPath struct {
	ProjectID string `json:"id" uri:"id" binding:"required,max=32"`
}
type taskPath struct {
	ProjectID string `json:"id" uri:"id" binding:"required,max=32"`
	TaskID    string `json:"task_id" uri:"task_id" binding:"required,max=64"`
}
type dependencyPath struct {
	ProjectID      string `json:"id" uri:"id" binding:"required,max=32"`
	TaskID         string `json:"task_id" uri:"task_id" binding:"required,max=64"`
	RequiredTaskID string `json:"required_task_id" uri:"required_task_id" binding:"required,max=64"`
}

func Register(router *gin.RouterGroup, service *Service, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	input, err := request.NewBody[Input]()
	if err != nil {
		return err
	}
	list, err := request.NewContract[ListInput]()
	if err != nil {
		return err
	}
	project, err := request.NewContract[projectPath]()
	if err != nil {
		return err
	}
	path, err := request.NewContract[taskPath]()
	if err != nil {
		return err
	}
	dependency, err := request.NewBody[DependencyInput]()
	if err != nil {
		return err
	}
	dependencyParams, err := request.NewContract[dependencyPath]()
	if err != nil {
		return err
	}
	dependencies, err := request.NewContract[DependencyListInput]()
	if err != nil {
		return err
	}
	relationships, err := request.NewContract[RelationshipInput]()
	if err != nil {
		return err
	}
	group := router.Group("/projects/:id/tasks", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	type endpoint struct {
		method, path string
		operation    openapi.Operation
		policy       ratelimit.Policy
		handler      gin.HandlerFunc
	}
	endpoints := []endpoint{
		{"POST", "", createContract(input.Schema, project.Schema), createTaskRateLimit, func(c *gin.Context) {
			params, err := project.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			value, err := input.Decode(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Create(c.Request.Context(), principal.User.ID, params.ProjectID, value)
			if err != nil {
				taskError(c, logger, err)
				return
			}
			c.Header("Location", "/projects/"+params.ProjectID+"/tasks/"+result.ID)
			c.JSON(http.StatusCreated, result)
		}},
		{"GET", "", listContract(project.Schema, list.Schema), listTasksRateLimit, func(c *gin.Context) {
			params, err := project.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			value, err := list.DecodeQuery(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.List(c.Request.Context(), principal.User.ID, params.ProjectID, value)
			if err != nil {
				taskError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"GET", "/:task_id", getContract(path.Schema), getTaskRateLimit, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Get(c.Request.Context(), principal.User.ID, params.ProjectID, params.TaskID)
			if err != nil {
				taskError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"PUT", "/:task_id", updateContract(input.Schema, path.Schema), updateTaskRateLimit, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			value, err := input.Decode(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Update(c.Request.Context(), principal.User.ID, params.ProjectID, params.TaskID, value)
			if err != nil {
				taskError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"DELETE", "/:task_id", removeContract(path.Schema), removeTaskRateLimit, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			if err := service.Remove(c.Request.Context(), principal.User.ID, params.ProjectID, params.TaskID); err != nil {
				taskError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
		{"POST", "/:task_id/dependencies", addDependencyContract(dependency.Schema, path.Schema), addDependencyRateLimit, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			value, err := dependency.Decode(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.AddDependency(c.Request.Context(), principal.User.ID, params.ProjectID, params.TaskID, value.RequiredTaskID)
			if err != nil {
				taskError(c, logger, err)
				return
			}
			c.JSON(http.StatusCreated, result)
		}},
		{"DELETE", "/:task_id/dependencies/:required_task_id", removeDependencyContract(dependencyParams.Schema), removeDependencyRateLimit, func(c *gin.Context) {
			params, err := dependencyParams.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			if err := service.RemoveDependency(c.Request.Context(), principal.User.ID, params.ProjectID, params.TaskID, params.RequiredTaskID); err != nil {
				taskError(c, logger, err)
				return
			}
			c.Status(http.StatusNoContent)
		}},
		{"GET", "/:task_id/dependencies", dependenciesContract(path.Schema, dependencies.Schema), listDependenciesRateLimit, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			value, err := dependencies.DecodeQuery(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Dependencies(c.Request.Context(), principal.User.ID, params.ProjectID, params.TaskID, value)
			if err != nil {
				taskError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"GET", "/:task_id/relationships", relationshipsContract(path.Schema, relationships.Schema), listRelationshipsRateLimit, func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			value, err := relationships.DecodeQuery(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Relationships(c.Request.Context(), principal.User.ID, params.ProjectID, params.TaskID, value)
			if err != nil {
				taskError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
	}
	for _, action := range []string{"start", "done", "reopen"} {
		endpoints = append(endpoints, endpoint{"POST", "/:task_id/" + action, transitionContract(path.Schema, action), taskActionRateLimits[action], func(c *gin.Context) {
			params, err := path.DecodePath(c)
			if err != nil {
				taskError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Transition(c.Request.Context(), principal.User.ID, params.ProjectID, params.TaskID, action)
			if err != nil {
				taskError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}})
	}
	for _, endpoint := range endpoints {
		limiter, err := ratelimit.New(endpoint.policy)
		if err != nil {
			return err
		}
		session, err := authentication.RequireRateLimitedSession(accounts, logger, endpoint.policy, enabled)
		if err != nil {
			return err
		}
		if err = contracts.Register(group, endpoint.method, endpoint.path, endpoint.operation, limiter.Middleware(enabled), session, endpoint.handler); err != nil {
			return err
		}
	}
	return nil
}
func taskError(c *gin.Context, logger *slog.Logger, err error) {
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
		logger.ErrorContext(c.Request.Context(), "task operation failed", "route", c.FullPath())
	}
	c.JSON(status, struct {
		Error string `json:"error"`
	}{message})
}

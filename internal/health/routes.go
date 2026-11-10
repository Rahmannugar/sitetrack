package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"sitetrack/internal/infra/openapi"
)

type Database interface {
	Ping(context.Context) error
}

type response struct {
	Status string `json:"status"`
}

func Register(router *gin.RouterGroup, database Database, contracts *openapi.Registry) error {
	responses := contractResponses()
	if err := contracts.Register(router, http.MethodGet, "/health/live", openapi.Operation{
		OperationID: "healthLiveness", Summary: "Check API process health", Tags: []string{"Health"},
		Responses: map[string]openapi.Response{"200": responses["200"]},
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, response{Status: "ok"})
	}); err != nil {
		return err
	}
	return contracts.Register(router, http.MethodGet, "/health/ready", openapi.Operation{
		OperationID: "healthReadiness", Summary: "Check PostgreSQL readiness", Tags: []string{"Health"},
		Responses: responses,
	}, func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, response{Status: "unavailable"})
			return
		}
		c.JSON(http.StatusOK, response{Status: "ok"})
	})
}

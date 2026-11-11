package main

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"sitetrack/internal/authentication"
	"sitetrack/internal/health"
	"sitetrack/internal/infra/httpserver"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/telemetry"
	"sitetrack/internal/notifications"
	"sitetrack/internal/projects"
	"sitetrack/internal/tasks"
)

func newRouter(database health.Database, monitoring *telemetry.Runtime, accounts authentication.Accounts, projectService *projects.Service, notificationService *notifications.Service, taskService *tasks.Service, rateLimitEnabled bool) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}
	observeHTTP, err := monitoring.HTTP()
	if err != nil {
		return nil, err
	}
	router.Use(observeHTTP)
	requests := router.Group("", httpserver.Timeout(15*time.Second))
	contracts := openapi.New()
	if err := health.Register(requests, database, contracts); err != nil {
		return nil, err
	}
	if err := authentication.Register(requests, accounts, contracts, rateLimitEnabled, monitoring.Logger); err != nil {
		return nil, err
	}
	if err := projects.Register(requests, projectService, accounts, contracts, rateLimitEnabled, monitoring.Logger); err != nil {
		return nil, err
	}
	if err := notifications.Register(requests, router.Group(""), notificationService, accounts, contracts, rateLimitEnabled, monitoring.Logger); err != nil {
		return nil, err
	}
	if err := tasks.Register(requests, taskService, accounts, contracts, rateLimitEnabled, monitoring.Logger); err != nil {
		return nil, err
	}
	if err := contracts.RegisterDocs(requests); err != nil {
		return nil, err
	}
	return router, nil
}

package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type Database interface {
	Ping(context.Context) error
}

type response struct {
	Status string `json:"status"`
}

func Register(router *gin.Engine, database Database) {
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, response{Status: "ok"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, response{Status: "unavailable"})
			return
		}
		c.JSON(http.StatusOK, response{Status: "ok"})
	})
}

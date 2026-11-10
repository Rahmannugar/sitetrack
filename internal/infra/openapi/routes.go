package openapi

import (
	_ "embed"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	SpecPath = "/api/openapi.json"
	DocsPath = "/api/docs"
)

//go:embed scalar.html
var scalarHTML []byte

func (r *Registry) RegisterDocs(router gin.IRoutes) error {
	document, err := r.JSON()
	if err != nil {
		return fmt.Errorf("encode OpenAPI document: %w", err)
	}
	router.GET(SpecPath, func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, "application/json; charset=utf-8", document)
	})
	router.GET(DocsPath, func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, "text/html; charset=utf-8", scalarHTML)
	})
	return nil
}

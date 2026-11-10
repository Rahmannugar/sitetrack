package openapi

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonschema-go/jsonschema"
)

type Document struct {
	OpenAPI string                          `json:"openapi"`
	Info    Info                            `json:"info"`
	Servers []Server                        `json:"servers"`
	Paths   map[string]map[string]Operation `json:"paths"`
}

type Info struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type Server struct {
	URL string `json:"url"`
}

type Operation struct {
	OperationID string              `json:"operationId"`
	Summary     string              `json:"summary"`
	Tags        []string            `json:"tags"`
	Responses   map[string]Response `json:"responses"`
	RequestBody *RequestBody        `json:"requestBody,omitempty"`
}

type RequestBody struct {
	Required bool                 `json:"required"`
	Content  map[string]MediaType `json:"content"`
}

type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content"`
}

type MediaType struct {
	Schema   *jsonschema.Schema `json:"schema"`
	Examples map[string]Example `json:"examples,omitempty"`
}

type Example struct {
	Summary string          `json:"summary"`
	Value   json.RawMessage `json:"value"`
}

type Registry struct {
	document Document
	ids      map[string]bool
}

func New() *Registry {
	return &Registry{
		document: Document{
			OpenAPI: "3.1.1",
			Info:    Info{Title: "SiteTrack API", Version: "0.1.0"},
			Servers: []Server{{URL: "/"}},
			Paths:   make(map[string]map[string]Operation),
		},
		ids: make(map[string]bool),
	}
}

func (r *Registry) Register(router *gin.RouterGroup, method, route string, operation Operation, handlers ...gin.HandlerFunc) error {
	mounted := path.Join(router.BasePath(), route)
	methodKey := strings.ToLower(method)
	if operation.OperationID == "" || operation.Summary == "" || len(operation.Responses) == 0 {
		return fmt.Errorf("incomplete OpenAPI operation for %s %s", method, mounted)
	}
	if r.ids[operation.OperationID] {
		return fmt.Errorf("duplicate OpenAPI operation ID %q", operation.OperationID)
	}
	if _, exists := r.document.Paths[mounted][methodKey]; exists {
		return fmt.Errorf("duplicate OpenAPI operation for %s %s", method, mounted)
	}
	if r.document.Paths[mounted] == nil {
		r.document.Paths[mounted] = make(map[string]Operation)
	}
	r.document.Paths[mounted][methodKey] = operation
	r.ids[operation.OperationID] = true
	router.Handle(method, route, handlers...)
	return nil
}

func (r *Registry) JSON() ([]byte, error) {
	return json.MarshalIndent(r.document, "", "  ")
}

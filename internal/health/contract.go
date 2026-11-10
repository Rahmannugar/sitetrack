package health

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"

	"sitetrack/internal/infra/openapi"
)

func contractResponses() map[string]openapi.Response {
	return map[string]openapi.Response{
		"200": {
			Description: "The health check succeeded.",
			Content: map[string]openapi.MediaType{
				"application/json": {
					Schema: &jsonschema.Schema{
						Type: "object", Required: []string{"status"},
						Properties: map[string]*jsonschema.Schema{
							"status": {Type: "string", Pattern: "^ok$"},
						},
					},
					Examples: map[string]openapi.Example{
						"healthy": {Summary: "Healthy", Value: json.RawMessage(`{"status":"ok"}`)},
					},
				},
			},
		},
		"503": {
			Description: "PostgreSQL could not be reached within the check timeout.",
			Content: map[string]openapi.MediaType{
				"application/json": {
					Schema: &jsonschema.Schema{
						Type: "object", Required: []string{"status"},
						Properties: map[string]*jsonschema.Schema{
							"status": {Type: "string", Pattern: "^unavailable$"},
						},
					},
					Examples: map[string]openapi.Example{
						"unavailable": {Summary: "PostgreSQL unavailable", Value: json.RawMessage(`{"status":"unavailable"}`)},
					},
				},
			},
		},
	}
}

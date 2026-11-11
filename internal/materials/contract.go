package materials

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"sitetrack/internal/infra/openapi"
)

var materialExample = json.RawMessage(`{"id":"019f2aa0-7000-7000-8000-000000000010","name":"Cement, OPC 50 kg bag","category_id":"019f2aa0-7000-7000-8000-000000000011","category_name":"Cement","unit":"bag","created_at":"2026-11-11T12:00:00Z","updated_at":"2026-11-11T12:00:00Z"}`)
var categoryExample = json.RawMessage(`{"id":"019f2aa0-7000-7000-8000-000000000011","name":"Cement","created_at":"2026-11-11T12:00:00Z","updated_at":"2026-11-11T12:00:00Z"}`)

func categorySchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"id", "name", "created_at", "updated_at"}, Properties: map[string]*jsonschema.Schema{
		"id": {Type: "string", Format: "uuid"}, "name": {Type: "string"}, "created_at": {Type: "string", Format: "date-time"}, "updated_at": {Type: "string", Format: "date-time"},
	}}
}
func materialSchema() *jsonschema.Schema {
	schema := categorySchema()
	schema.Required = append(schema.Required, "category_id", "category_name", "unit")
	schema.Properties["category_id"] = &jsonschema.Schema{Type: "string", Format: "uuid"}
	schema.Properties["category_name"] = &jsonschema.Schema{Type: "string"}
	schema.Properties["unit"] = &jsonschema.Schema{Type: "string"}
	return schema
}
func response(description string, schema *jsonschema.Schema, example json.RawMessage) openapi.Response {
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{"application/json": {Schema: schema, Examples: map[string]openapi.Example{"result": {Summary: description, Value: example}}}}}
}
func catalogueContract(id, summary string, body, path, query *jsonschema.Schema, write bool) openapi.Operation {
	op := openapi.Operation{OperationID: id, Summary: summary, Tags: []string{"Material catalogue"}, Security: []map[string][]string{{"bearerAuth": {}}}, Responses: map[string]openapi.Response{}}
	for _, entry := range []struct{ status, message string }{{"400", ErrInvalid.Error()}, {"401", "authentication required"}, {"429", "rate limit exceeded"}, {"500", "internal server error"}, {"504", "request timed out"}} {
		op.Responses[entry.status] = errorContract(entry.message)
	}
	if write {
		op.Responses["403"] = errorContract(ErrForbidden.Error())
		op.Responses["409"] = errorContract(ErrConflict.Error() + ": name already in use")
	}
	if path != nil || id == "createMaterial" || id == "updateMaterial" {
		op.Responses["404"] = errorContract(ErrNotFound.Error())
	}
	if body != nil {
		op.RequestBody = &openapi.RequestBody{Required: true, Content: map[string]openapi.MediaType{"application/json": {Schema: body}}}
	}
	if path != nil {
		op.Parameters = openapi.Parameters(path, "path")
	}
	if query != nil {
		op.Parameters = append(op.Parameters, openapi.Parameters(query, "query")...)
	}
	return op
}
func errorContract(message string) openapi.Response {
	raw, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{message})
	return response(message, &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, raw)
}
func resourceContract(id, summary string, body, path *jsonschema.Schema, categories, create bool) openapi.Operation {
	op := catalogueContract(id, summary, body, path, nil, body != nil)
	schema, example := materialSchema(), materialExample
	if categories {
		schema, example = categorySchema(), categoryExample
	}
	status, description := "200", "Catalogue details."
	if body != nil {
		description = "Updated. Company store managers and SuperAdmins can make changes."
	}
	if create {
		status, description = "201", "Created. Company store managers and SuperAdmins can create catalogue entries."
	}
	op.Responses[status] = response(description, schema, example)
	return op
}
func listingContract(id, summary string, query *jsonschema.Schema, categories bool) openapi.Operation {
	op := catalogueContract(id, summary, nil, nil, query, false)
	schema, example := materialSchema(), materialExample
	if categories {
		schema, example = categorySchema(), categoryExample
	}
	page := &jsonschema.Schema{Type: "object", Required: []string{"items", "next_cursor"}, Properties: map[string]*jsonschema.Schema{"items": {Type: "array", Items: schema}, "next_cursor": {Type: "string"}}}
	op.Responses["200"] = response("Ordered by name. Pages default to 20, maximum 100. Search matches literal text without regard to case. Keep the same filters with next_cursor.", page, json.RawMessage(`{"items":[`+string(example)+`],"next_cursor":""}`))
	return op
}
func removalContract(id, summary string, path *jsonschema.Schema, categories bool) openapi.Operation {
	op := catalogueContract(id, summary, nil, path, nil, true)
	delete(op.Responses, "409")
	description := "Material removed from the catalogue. Its record is retained."
	if categories {
		description = "Category removed. Its record is retained."
		op.Responses["409"] = errorContract(ErrConflict.Error() + ": category still has materials")
	}
	op.Responses["204"] = openapi.Response{Description: description}
	return op
}

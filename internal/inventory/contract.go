package inventory

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"sitetrack/internal/infra/openapi"
)

var companyExample = json.RawMessage(`{"material_id":"019f2aa0-7000-7000-8000-000000000010","name":"Cement, OPC 50 kg bag","category_id":"019f2aa0-7000-7000-8000-000000000011","category_name":"Cement","unit":"bag","quantity":"12.500","reorder_point":"20.000","in_transit_to_projects":"0.000","status":"low_stock","updated_at":"2026-11-11T12:00:00Z"}`)
var projectExample = json.RawMessage(`{"store_id":"019f2aa0-7000-7000-8000-000000000002","material_id":"019f2aa0-7000-7000-8000-000000000010","name":"Cement, OPC 50 kg bag","category_id":"019f2aa0-7000-7000-8000-000000000011","category_name":"Cement","unit":"bag","quantity":"8.000","last_delivery_at":"2026-11-11T12:00:00Z","updated_at":"2026-11-11T12:00:00Z"}`)

func quantitySchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: `^[0-9]{1,15}\.[0-9]{3}$`}
}
func itemSchema(company bool) *jsonschema.Schema {
	schema := &jsonschema.Schema{Type: "object", Required: []string{"material_id", "name", "category_id", "category_name", "unit", "quantity", "updated_at"}, Properties: map[string]*jsonschema.Schema{
		"material_id": {Type: "string", Format: "uuid"}, "name": {Type: "string"}, "category_id": {Type: "string", Format: "uuid"}, "category_name": {Type: "string"}, "unit": {Type: "string"}, "quantity": quantitySchema(), "updated_at": {Type: "string", Format: "date-time"},
	}}
	if company {
		schema.Required = append(schema.Required, "reorder_point", "in_transit_to_projects", "status")
		schema.Properties["reorder_point"] = quantitySchema()
		schema.Properties["in_transit_to_projects"] = quantitySchema()
		schema.Properties["status"] = &jsonschema.Schema{Type: "string", Pattern: "^(out_of_stock|low_stock|available)$"}
	} else {
		schema.Required = append(schema.Required, "store_id", "last_delivery_at")
		schema.Properties["store_id"] = &jsonschema.Schema{Type: "string", Format: "uuid"}
		schema.Properties["last_delivery_at"] = &jsonschema.Schema{Types: []string{"string", "null"}, Format: "date-time"}
	}
	return schema
}
func response(description string, schema *jsonschema.Schema, example json.RawMessage) openapi.Response {
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{"application/json": {Schema: schema, Examples: map[string]openapi.Example{"result": {Summary: description, Value: example}}}}}
}
func errorContract(message string) openapi.Response {
	raw, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{message})
	return response(message, &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, raw)
}
func operation(id, summary string, body, path, query *jsonschema.Schema, company bool) openapi.Operation {
	op := openapi.Operation{OperationID: id, Summary: summary, Tags: []string{"Stores"}, Security: []map[string][]string{{"bearerAuth": {}}}, Responses: map[string]openapi.Response{}}
	for _, entry := range []struct{ status, message string }{{"400", ErrInvalid.Error()}, {"401", "authentication required"}, {"404", ErrNotFound.Error()}, {"429", "rate limit exceeded"}, {"500", "internal server error"}, {"504", "request timed out"}} {
		op.Responses[entry.status] = errorContract(entry.message)
	}
	if company {
		op.Responses["403"] = errorContract(ErrForbidden.Error())
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
func itemContract(id, summary string, body, path *jsonschema.Schema, company bool) openapi.Operation {
	op := operation(id, summary, body, path, nil, company)
	example := projectExample
	if company {
		example = companyExample
	}
	description := "Current stock. Quantities are decimal strings with three decimal places."
	if id == "addCompanyStock" {
		description = "Stock added to the company store. quantity must be greater than zero, with at most three decimal places. Each successful request adds the supplied amount."
		op.Responses["409"] = errorContract(ErrConflict.Error())
	}
	if id == "setCompanyReorderPoint" {
		description = "Reorder point updated. Supply a nonnegative decimal string with at most three decimal places."
	}
	op.Responses["200"] = response(description, itemSchema(company), example)
	return op
}
func listContract(path, query *jsonschema.Schema, company bool) openapi.Operation {
	id, summary := "getProjectStore", "View the project store"
	example := projectExample
	if company {
		id, summary = "getCompanyStore", "View the company store"
		example = companyExample
	}
	op := operation(id, summary, nil, path, query, company)
	schema := &jsonschema.Schema{Type: "object", Required: []string{"items", "next_cursor"}, Properties: map[string]*jsonschema.Schema{"items": {Type: "array", Items: itemSchema(company)}, "next_cursor": {Type: "string"}}}
	description := "Search by name and filter by category. Pages default to 20, maximum 100. Keep the same filters and sort with next_cursor. Last-delivery sorting puts undated entries last."
	if company {
		description = "All active catalogue materials, including zero stock. Filter by category or stock status and search by name. Pages default to 20, maximum 100. Keep the same filters and sort with next_cursor."
	}
	op.Responses["200"] = response(description, schema, json.RawMessage(`{"items":[`+string(example)+`],"next_cursor":""}`))
	return op
}

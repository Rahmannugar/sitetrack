package deliveries

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"sitetrack/internal/infra/openapi"
	"strings"
)

var deliveryExample = json.RawMessage(`{"id":"019f2aa0-7000-7000-8000-000000000030","project_id":"PRJ-0001","project_name":"Riverside Site","direction":"company_to_project","status":"in_transit","initiated_by_user_id":"019f2aa0-7000-7000-8000-000000000001","initiated_by_name":"Store Manager","completed_by_user_id":null,"completed_by_name":null,"created_at":"2026-11-11T12:00:00Z","completed_at":null,"items":[{"material_id":"019f2aa0-7000-7000-8000-000000000010","name":"Cement, OPC 50 kg bag","unit":"bag","quantity":"10.125"}]}`)

func deliverySchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"id", "project_id", "project_name", "direction", "status", "initiated_by_user_id", "initiated_by_name", "completed_by_user_id", "completed_by_name", "created_at", "completed_at", "items"}, Properties: map[string]*jsonschema.Schema{
		"id": {Type: "string", Format: "uuid"}, "project_id": {Type: "string"}, "project_name": {Type: "string"}, "direction": {Type: "string", Pattern: "^(company_to_project|project_to_company)$"}, "status": {Type: "string", Pattern: "^(in_transit|delivered|rejected|cancelled)$"},
		"initiated_by_user_id": {Type: "string", Format: "uuid"}, "initiated_by_name": {Type: "string"}, "completed_by_user_id": {Types: []string{"string", "null"}, Format: "uuid"}, "completed_by_name": {Types: []string{"string", "null"}}, "created_at": {Type: "string", Format: "date-time"}, "completed_at": {Types: []string{"string", "null"}, Format: "date-time", Description: "Time the delivery was received, rejected or cancelled."},
		"items": {Type: "array", Items: &jsonschema.Schema{Type: "object", Required: []string{"material_id", "name", "unit", "quantity"}, Properties: map[string]*jsonschema.Schema{"material_id": {Type: "string", Format: "uuid"}, "name": {Type: "string"}, "unit": {Type: "string"}, "quantity": {Type: "string", Pattern: `^[0-9]{1,15}\.[0-9]{3}$`}}}},
	}}
}
func response(description string, schema *jsonschema.Schema, example json.RawMessage) openapi.Response {
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{"application/json": {Schema: schema, Examples: map[string]openapi.Example{"result": {Summary: description, Value: example}}}}}
}
func operation(id, summary string, path, query, body *jsonschema.Schema) openapi.Operation {
	op := openapi.Operation{OperationID: id, Summary: summary, Tags: []string{"Deliveries"}, Security: []map[string][]string{{"bearerAuth": {}}}, Responses: map[string]openapi.Response{}}
	for _, item := range []struct{ status, message string }{{"400", ErrInvalid.Error()}, {"401", "authentication required"}, {"403", ErrForbidden.Error()}, {"404", ErrNotFound.Error()}, {"429", "rate limit exceeded"}, {"500", "internal server error"}, {"504", "request timed out"}} {
		raw, _ := json.Marshal(struct {
			Error string `json:"error"`
		}{item.message})
		op.Responses[item.status] = response(item.message, &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, raw)
	}
	if path != nil {
		op.Parameters = openapi.Parameters(path, "path")
	}
	if query != nil {
		op.Parameters = append(op.Parameters, openapi.Parameters(query, "query")...)
	}
	if body != nil {
		op.RequestBody = &openapi.RequestBody{Required: true, Content: map[string]openapi.MediaType{"application/json": {Schema: body}}}
	}
	return op
}
func conflicts(op *openapi.Operation, messages ...error) {
	examples := map[string]openapi.Example{}
	for _, err := range messages {
		raw, _ := json.Marshal(struct {
			Error string `json:"error"`
		}{err.Error()})
		examples[strings.ReplaceAll(err.Error(), " ", "_")] = openapi.Example{Summary: err.Error(), Value: raw}
	}
	op.Responses["409"] = openapi.Response{Description: "The delivery or stock cannot make this change.", Content: map[string]openapi.MediaType{"application/json": {Schema: &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, Examples: examples}}}
}
func createContract(body *jsonschema.Schema) openapi.Operation {
	op := operation("createDelivery", "Send a delivery", nil, nil, body)
	op.Responses["201"] = response("Delivery sent. Stock leaves the sender immediately. Supply 1–100 distinct materials with positive decimal quantities. Company store managers and SuperAdmins send from company; project admins send from their project.", deliverySchema(), deliveryExample)
	conflicts(&op, ErrStock, ErrCapacity, ErrMaterial)
	return op
}
func detailContract(path *jsonschema.Schema) openapi.Operation {
	op := operation("getDelivery", "View a delivery", path, nil, nil)
	op.Responses["200"] = response("Delivery and all its material lines.", deliverySchema(), deliveryExample)
	return op
}
func transitionContract(action string, path *jsonschema.Schema) openapi.Operation {
	status := map[string]string{"receive": "delivered", "reject": "rejected", "cancel": "cancelled"}[action]
	title := strings.ToUpper(action[:1]) + action[1:]
	op := operation(action+"Delivery", title+" a delivery", path, nil, nil)
	example := strings.Replace(string(deliveryExample), `"status":"in_transit"`, `"status":"`+status+`"`, 1)
	example = strings.Replace(example, `"completed_by_user_id":null`, `"completed_by_user_id":"019f2aa0-7000-7000-8000-000000000002"`, 1)
	example = strings.Replace(example, `"completed_by_name":null`, `"completed_by_name":"Project Admin"`, 1)
	example = strings.Replace(example, `"completed_at":null`, `"completed_at":"2026-11-11T13:00:00Z"`, 1)
	description := "The receiver records arrival and stock joins its store. Project receipt requires a project admin; company receipt requires a company store manager or SuperAdmin."
	if action == "reject" {
		description = "The receiver rejects an in-transit delivery and stock returns to the sender."
	}
	if action == "cancel" {
		description = "The sender cancels an in-transit delivery and stock returns to its store."
		example = strings.Replace(example, "Project Admin", "Store Manager", 1)
	}
	op.Responses["200"] = response(description, deliverySchema(), json.RawMessage(example))
	conflicts(&op, ErrState, ErrCapacity, ErrMaterial)
	return op
}
func listContract(path, query *jsonschema.Schema, company bool) openapi.Operation {
	id, summary := "listProjectDeliveries", "List project deliveries"
	if company {
		id, summary = "listCompanyDeliveries", "List company deliveries"
	}
	op := operation(id, summary, path, query, nil)
	schema := &jsonschema.Schema{Type: "object", Required: []string{"items", "next_cursor"}, Properties: map[string]*jsonschema.Schema{"items": {Type: "array", Items: deliverySchema()}, "next_cursor": {Type: "string"}}}
	op.Responses["200"] = response("Newest first. Filter by direction, status and sent date range (UTC, inclusive dates). Company lists also filter by project_id. Limit defaults to 20, maximum 100. Keep the same filters when using next_cursor.", schema, json.RawMessage(`{"items":[`+string(deliveryExample)+`],"next_cursor":""}`))
	return op
}

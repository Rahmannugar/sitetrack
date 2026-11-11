package projects

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"sitetrack/internal/infra/openapi"
)

var projectExample = json.RawMessage(`{"id":"PRJ-0001","name":"Riverside Site","start_date":"2026-11-10","end_date":"2027-02-10","store_id":"019f2aa0-7000-7000-8000-000000000002","is_admin":true,"member_count":1,"task_count":0,"not_started_count":0,"in_progress_count":0,"done_count":0,"late_count":0,"blocked_count":0,"completion_percentage":0,"created_at":"2026-11-10T12:00:00Z"}`)
var memberExample = json.RawMessage(`{"user_id":"019f2aa0-7000-7000-8000-000000000001","name":"Site Engineer","is_admin":true,"joined_at":"2026-11-10T12:00:00Z"}`)

func projectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"id", "name", "start_date", "end_date", "store_id", "is_admin", "member_count", "task_count", "not_started_count", "in_progress_count", "done_count", "late_count", "blocked_count", "completion_percentage", "created_at"}, Properties: map[string]*jsonschema.Schema{
		"id": {Type: "string", Pattern: `^PRJ-[0-9]{4,}$`}, "name": {Type: "string"}, "start_date": {Type: "string", Format: "date"}, "end_date": {Type: "string", Format: "date"}, "store_id": {Type: "string", Format: "uuid"}, "is_admin": {Type: "boolean"}, "member_count": {Type: "integer"}, "task_count": {Type: "integer"}, "not_started_count": {Type: "integer"}, "in_progress_count": {Type: "integer"}, "done_count": {Type: "integer"}, "late_count": {Type: "integer"}, "blocked_count": {Type: "integer"}, "completion_percentage": {Type: "number"}, "created_at": {Type: "string", Format: "date-time"},
	}}
}
func memberSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"user_id", "name", "is_admin", "joined_at"}, Properties: map[string]*jsonschema.Schema{
		"user_id": {Type: "string", Format: "uuid"}, "name": {Type: "string"}, "is_admin": {Type: "boolean"}, "joined_at": {Type: "string", Format: "date-time"},
	}}
}
func response(description string, schema *jsonschema.Schema, example json.RawMessage) openapi.Response {
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{"application/json": {Schema: schema, Examples: map[string]openapi.Example{"example": {Summary: description, Value: example}}}}}
}
func projectOperation(id, summary string, body *jsonschema.Schema, path *jsonschema.Schema, admin bool) openapi.Operation {
	responses := map[string]openapi.Response{}
	for _, entry := range []struct{ code, description, message string }{
		{"400", "Invalid input, date range or cursor.", ErrInvalid.Error()},
		{"401", "A current authenticated session is required.", "authentication required"},
		{"429", "Endpoint request limit exceeded.", "rate limit exceeded"},
		{"500", "The operation could not complete.", "internal server error"},
		{"504", "The operation timed out.", "request timed out"},
	} {
		example, _ := json.Marshal(struct {
			Error string `json:"error"`
		}{entry.message})
		responses[entry.code] = response(entry.description, &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, example)
	}
	if path != nil {
		responses["404"] = response("Project or member not found.", errorSchema(), json.RawMessage(`{"error":"project or member not found"}`))
	}
	if admin {
		responses["403"] = response("Project admin required.", errorSchema(), json.RawMessage(`{"error":"project admin required"}`))
	}
	op := openapi.Operation{OperationID: id, Summary: summary, Tags: []string{"Projects"}, Security: []map[string][]string{{"bearerAuth": {}}}, Responses: responses}
	if body != nil {
		op.RequestBody = &openapi.RequestBody{Required: true, Content: map[string]openapi.MediaType{"application/json": {Schema: body}}}
	}
	if path != nil {
		op.Parameters = openapi.Parameters(path, "path")
	}
	return op
}
func errorSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}
}
func createContract(body *jsonschema.Schema) openapi.Operation {
	op := projectOperation("createProject", "Create a project", body, nil, false)
	op.Responses["201"] = response("Project created with its store. You are its first admin.", projectSchema(), projectExample)
	return op
}
func readContract(path *jsonschema.Schema) openapi.Operation {
	op := projectOperation("getProject", "View a project", nil, path, false)
	op.Responses["200"] = response("Project details, task counts and your project admin status.", projectSchema(), projectExample)
	return op
}
func updateContract(body, path *jsonschema.Schema) openapi.Operation {
	op := projectOperation("updateProject", "Replace project name and planned dates", body, path, true)
	op.Responses["200"] = response("Project details updated.", projectSchema(), projectExample)
	return op
}
func removeContract(path *jsonschema.Schema) openapi.Operation {
	op := projectOperation("removeProject", "Remove a project", nil, path, true)
	op.Responses["204"] = openapi.Response{Description: "Project removed. Its code is never reused."}
	op.Responses["409"] = response(ErrStock.Error(), errorSchema(), json.RawMessage(`{"error":"project store still has stock or goods in transit"}`))
	return op
}
func pageSchema(item *jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"items", "next_cursor"}, Properties: map[string]*jsonschema.Schema{"items": {Type: "array", Items: item}, "next_cursor": {Type: "string"}}}
}
func listContract(query *jsonschema.Schema) openapi.Operation {
	op := projectOperation("listProjects", "List projects", nil, nil, false)
	op.Parameters = openapi.Parameters(query, "query")
	op.Responses["200"] = response("Projects with task counts and your admin status. Filter by search or late_only, and sort by id, name, start_date, completion_percentage or late_count. Pages default to 20, maximum 100; keep the same filters and sort with next_cursor.", pageSchema(projectSchema()), json.RawMessage(`{"items":[`+string(projectExample)+`],"next_cursor":""}`))
	return op
}
func membersContract(query, path *jsonschema.Schema) openapi.Operation {
	op := projectOperation("listProjectMembers", "List project members", nil, path, false)
	op.Parameters = append(op.Parameters, openapi.Parameters(query, "query")...)
	op.Responses["200"] = response("Members ordered by user id. Limit defaults to 20, maximum 100. Use next_cursor to continue; empty cursor ends the list.", pageSchema(memberSchema()), json.RawMessage(`{"items":[`+string(memberExample)+`],"next_cursor":""}`))
	return op
}
func changeMemberContract(body, path *jsonschema.Schema) openapi.Operation {
	op := projectOperation("changeProjectMember", "Change an existing member's admin status", body, path, true)
	op.Responses["204"] = openapi.Response{Description: "Admin status updated. This operation cannot add members."}
	op.Responses["409"] = response("The last admin cannot be demoted.", errorSchema(), json.RawMessage(`{"error":"project must retain at least one admin"}`))
	return op
}
func removeMemberContract(path *jsonschema.Schema) openapi.Operation {
	op := projectOperation("removeProjectMember", "Remove an existing project member", nil, path, true)
	op.Responses["204"] = openapi.Response{Description: "Member removed."}
	op.Responses["409"] = response("The last admin cannot be removed.", errorSchema(), json.RawMessage(`{"error":"project must retain at least one admin"}`))
	return op
}

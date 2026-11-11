package tasks

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"sitetrack/internal/infra/openapi"
)

var taskExample = json.RawMessage(`{"id":"PRJ-0001-T-0001","project_id":"PRJ-0001","name":"Pour slab","start_date":"2026-11-10","end_date":"2026-11-20","status":"not_started","late":false,"blocked":false,"depends_on":[],"dependency_count":0,"dependent_count":0,"has_more_dependencies":false,"allowed_actions":["start"],"created_at":"2026-11-10T12:00:00Z"}`)
var dependencyExample = json.RawMessage(`{"prerequisite_id":"PRJ-0001-T-0001","name":"Pour slab","status":"waiting","task_status":"not_started"}`)

func taskSchema() *jsonschema.Schema {
	maxDependencies := 20
	return &jsonschema.Schema{Type: "object", Required: []string{"id", "project_id", "name", "start_date", "end_date", "status", "late", "blocked", "depends_on", "dependency_count", "dependent_count", "has_more_dependencies", "allowed_actions", "created_at"}, Properties: map[string]*jsonschema.Schema{
		"id": {Type: "string", Pattern: `^PRJ-[0-9]{4,}-T-[0-9]{4,}$`}, "project_id": {Type: "string"}, "name": {Type: "string"}, "start_date": {Type: "string", Format: "date"}, "end_date": {Type: "string", Format: "date"}, "status": {Type: "string", Pattern: "^(not_started|in_progress|done)$"}, "late": {Type: "boolean"}, "blocked": {Type: "boolean"}, "depends_on": {Type: "array", Items: &jsonschema.Schema{Type: "string"}, MaxItems: &maxDependencies}, "dependency_count": {Type: "integer"}, "dependent_count": {Type: "integer"}, "has_more_dependencies": {Type: "boolean"}, "allowed_actions": {Type: "array", Items: &jsonschema.Schema{Type: "string", Pattern: "^(start|done|reopen)$"}}, "created_at": {Type: "string", Format: "date-time"},
	}}
}
func dependencySchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"prerequisite_id", "name", "status", "task_status"}, Properties: map[string]*jsonschema.Schema{"prerequisite_id": {Type: "string"}, "name": {Type: "string"}, "status": {Type: "string", Pattern: "^(waiting|satisfied)$"}, "task_status": {Type: "string", Pattern: "^(not_started|in_progress|done)$"}}}
}
func relationSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"id", "name", "status", "steps"}, Properties: map[string]*jsonschema.Schema{"id": {Type: "string"}, "name": {Type: "string"}, "status": {Type: "string", Pattern: "^(not_started|in_progress|done)$"}, "steps": {Type: "integer"}}}
}
func pageSchema(item *jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"items", "next_cursor"}, Properties: map[string]*jsonschema.Schema{"items": {Type: "array", Items: item}, "next_cursor": {Type: "string"}}}
}
func response(description string, schema *jsonschema.Schema, value json.RawMessage) openapi.Response {
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{"application/json": {Schema: schema, Examples: map[string]openapi.Example{"example": {Summary: description, Value: value}}}}}
}
func operation(id, summary string, body, path, query *jsonschema.Schema) openapi.Operation {
	responses := map[string]openapi.Response{}
	for _, entry := range []struct{ code, message string }{{"400", ErrInvalid.Error()}, {"401", "authentication required"}, {"404", ErrNotFound.Error()}, {"409", ErrConflict.Error() + ": task action is not allowed"}, {"429", "rate limit exceeded"}, {"500", "internal server error"}, {"504", "request timed out"}} {
		raw, _ := json.Marshal(struct {
			Error string `json:"error"`
		}{entry.message})
		responses[entry.code] = response(entry.message, &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, raw)
	}
	op := openapi.Operation{OperationID: id, Summary: summary, Tags: []string{"Tasks"}, Security: []map[string][]string{{"bearerAuth": {}}}, Responses: responses, Parameters: openapi.Parameters(path, "path")}
	if body != nil {
		op.RequestBody = &openapi.RequestBody{Required: true, Content: map[string]openapi.MediaType{"application/json": {Schema: body}}}
	}
	if query != nil {
		op.Parameters = append(op.Parameters, openapi.Parameters(query, "query")...)
	}
	return op
}
func createContract(body, path *jsonschema.Schema) openapi.Operation {
	op := operation("createTask", "Create a project task", body, path, nil)
	op.Responses["201"] = response("Members and SuperAdmins create tasks. Immutable task codes count up within the project and are never reused. Dates require end on or after start. Notification rows commit with the task.", taskSchema(), taskExample)
	return op
}
func getContract(path *jsonschema.Schema) openapi.Operation {
	op := operation("getTask", "Read a task and its allowed actions", nil, path, nil)
	delete(op.Responses, "409")
	op.Responses["200"] = response("Late uses today's UTC date. Blocked reflects unfinished prerequisites. depends_on contains the first twenty direct prerequisite codes; dependency_count and has_more_dependencies identify additional rows available through the dependencies endpoint. Allowed actions reflect current state and graph constraints.", taskSchema(), taskExample)
	return op
}
func listContract(path, query *jsonschema.Schema) openapi.Operation {
	op := operation("listTasks", "List project tasks", nil, path, query)
	delete(op.Responses, "409")
	op.Responses["200"] = response("Default twenty, maximum one hundred. Filter status, Late, Blocked and inclusive planned-interval overlap with from_date/to_date. Sort id/name/start_date/end_date/dependent_count, ascending or descending with id as tie-breaker. Continue with the same filters and sort. Cursors expire when the UTC date changes. Depends-on previews are bounded to twenty.", pageSchema(taskSchema()), json.RawMessage(`{"items":[`+string(taskExample)+`],"next_cursor":""}`))
	return op
}
func updateContract(body, path *jsonschema.Schema) openapi.Operation {
	op := operation("updateTask", "Replace task name and planned dates", body, path, nil)
	op.Responses["200"] = response("Task details updated; code and state stay unchanged. Status, Late and Blocked are not accepted as input.", taskSchema(), taskExample)
	return op
}
func removeContract(path *jsonschema.Schema) openapi.Operation {
	op := operation("removeTask", "Remove a task", nil, path, nil)
	op.Responses["204"] = openapi.Response{Description: "The task is hidden and its code retained. Outgoing dependencies are removed atomically. Referenced tasks cannot be removed until incoming dependency links are detached."}
	return op
}
func transitionContract(path *jsonschema.Schema, action string) openapi.Operation {
	op := operation("taskAction_"+action, "Apply task action: "+action, nil, path, nil)
	example := `{"id":"PRJ-0001-T-0001","project_id":"PRJ-0001","name":"Pour slab","start_date":"2026-11-10","end_date":"2026-11-20","status":"in_progress","late":false,"blocked":false,"depends_on":[],"dependency_count":0,"dependent_count":0,"has_more_dependencies":false,"allowed_actions":["done"],"created_at":"2026-11-10T12:00:00Z"}`
	description := "Start requires Not started and all reachable prerequisites done."
	if action == "done" {
		description = "Mark as done requires In progress; dependent links become satisfied in the same transaction."
		example = `{"id":"PRJ-0001-T-0001","project_id":"PRJ-0001","name":"Pour slab","start_date":"2026-11-10","end_date":"2026-11-20","status":"done","late":false,"blocked":false,"depends_on":[],"dependency_count":0,"dependent_count":0,"has_more_dependencies":false,"allowed_actions":["reopen"],"created_at":"2026-11-10T12:00:00Z"}`
	}
	if action == "reopen" {
		description = "Reopen requires Done and no started direct or transitive dependent. Dependency state returns to waiting atomically."
	}
	op.Responses["200"] = response(description, taskSchema(), json.RawMessage(example))
	return op
}
func addDependencyContract(body, path *jsonschema.Schema) openapi.Operation {
	op := operation("addTaskDependency", "Add a prerequisite", body, path, nil)
	op.Responses["201"] = response("Both tasks must be active in the same project. Self-links, duplicates and cycles are rejected. Started/done tasks cannot gain unfinished prerequisites. Stored status is waiting until the prerequisite is done, then satisfied; clients cannot set it.", dependencySchema(), dependencyExample)
	return op
}
func removeDependencyContract(path *jsonschema.Schema) openapi.Operation {
	op := operation("removeTaskDependency", "Remove a prerequisite", nil, path, nil)
	op.Responses["204"] = openapi.Response{Description: "Dependency removed and task activity notification created in the same transaction."}
	return op
}
func dependenciesContract(path, query *jsonschema.Schema) openapi.Operation {
	op := operation("listTaskDependencies", "List direct prerequisites", nil, path, query)
	delete(op.Responses, "409")
	op.Responses["200"] = response("Direct prerequisite codes, stored dependency state and current prerequisite task state. Ordered by prerequisite code, default twenty and maximum one hundred.", pageSchema(dependencySchema()), json.RawMessage(`{"items":[`+string(dependencyExample)+`],"next_cursor":""}`))
	return op
}
func relationshipsContract(path, query *jsonschema.Schema) openapi.Operation {
	op := operation("listTaskRelationships", "List reachable prerequisites or dependents", nil, path, query)
	delete(op.Responses, "409")
	op.Responses["200"] = response("Every reachable task appears once, with its shortest distance in steps. Choose prerequisites (default) or dependents. Ordered by steps then code; cursor pages default to twenty, maximum one hundred.", pageSchema(relationSchema()), json.RawMessage(`{"items":[{"id":"PRJ-0001-T-0001","name":"Pour slab","status":"not_started","steps":1}],"next_cursor":""}`))
	return op
}

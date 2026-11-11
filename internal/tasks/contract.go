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
	op.Responses["201"] = response("Task created. Its code is never changed or reused.", taskSchema(), taskExample)
	return op
}
func getContract(path *jsonschema.Schema) openapi.Operation {
	op := operation("getTask", "Read a task and its allowed actions", nil, path, nil)
	delete(op.Responses, "409")
	op.Responses["200"] = response("Task details and the actions you can take now. Late uses the UTC date. Blocked means a prerequisite is unfinished. depends_on previews up to 20 prerequisites; use the dependencies endpoint for the rest.", taskSchema(), taskExample)
	return op
}
func listContract(path, query *jsonschema.Schema) openapi.Operation {
	op := operation("listTasks", "List project tasks", nil, path, query)
	delete(op.Responses, "409")
	op.Responses["200"] = response("Filter by status, late, blocked, search or planned dates. Dates select tasks whose planned period overlaps the range. Pages default to 20, maximum 100. Keep the same filters and sort with next_cursor; start a new list after the UTC date changes.", pageSchema(taskSchema()), json.RawMessage(`{"items":[`+string(taskExample)+`],"next_cursor":""}`))
	return op
}
func updateContract(body, path *jsonschema.Schema) openapi.Operation {
	op := operation("updateTask", "Replace task name and planned dates", body, path, nil)
	op.Responses["200"] = response("Task name and dates updated. Use the task actions to change status.", taskSchema(), taskExample)
	return op
}
func removeContract(path *jsonschema.Schema) openapi.Operation {
	op := operation("removeTask", "Remove a task", nil, path, nil)
	op.Responses["204"] = openapi.Response{Description: "Task removed. Remove links from tasks that depend on it first. Its code is never reused."}
	return op
}
func transitionContract(path *jsonschema.Schema, action string) openapi.Operation {
	op := operation("taskAction_"+action, map[string]string{"start": "Start a task", "done": "Mark a task as done", "reopen": "Reopen a task"}[action], nil, path, nil)
	example := `{"id":"PRJ-0001-T-0001","project_id":"PRJ-0001","name":"Pour slab","start_date":"2026-11-10","end_date":"2026-11-20","status":"in_progress","late":false,"blocked":false,"depends_on":[],"dependency_count":0,"dependent_count":0,"has_more_dependencies":false,"allowed_actions":["done"],"created_at":"2026-11-10T12:00:00Z"}`
	description := "Start a task that has not started. All its prerequisites must be done."
	if action == "done" {
		description = "Complete an in-progress task. Its dependency links become satisfied in the same transaction."
		example = `{"id":"PRJ-0001-T-0001","project_id":"PRJ-0001","name":"Pour slab","start_date":"2026-11-10","end_date":"2026-11-20","status":"done","late":false,"blocked":false,"depends_on":[],"dependency_count":0,"dependent_count":0,"has_more_dependencies":false,"allowed_actions":["reopen"],"created_at":"2026-11-10T12:00:00Z"}`
	}
	if action == "reopen" {
		description = "Reopen a completed task if none of the tasks depending on it have started, including through other tasks. Its dependency links return to waiting in the same transaction."
	}
	op.Responses["200"] = response(description, taskSchema(), json.RawMessage(example))
	return op
}
func addDependencyContract(body, path *jsonschema.Schema) openapi.Operation {
	op := operation("addTaskDependency", "Add a prerequisite", body, path, nil)
	op.Responses["201"] = response("Add a prerequisite from the same project. Dependencies cannot form a cycle. A started or completed task cannot gain an unfinished prerequisite. Status is waiting or satisfied, based on whether the prerequisite is done.", dependencySchema(), dependencyExample)
	return op
}
func removeDependencyContract(path *jsonschema.Schema) openapi.Operation {
	op := operation("removeTaskDependency", "Remove a prerequisite", nil, path, nil)
	op.Responses["204"] = openapi.Response{Description: "Prerequisite removed."}
	return op
}
func dependenciesContract(path, query *jsonschema.Schema) openapi.Operation {
	op := operation("listTaskDependencies", "List task prerequisites", nil, path, query)
	delete(op.Responses, "409")
	op.Responses["200"] = response("Tasks this task depends on directly, with their task and dependency status. Ordered by code. Pages default to 20, maximum 100.", pageSchema(dependencySchema()), json.RawMessage(`{"items":[`+string(dependencyExample)+`],"next_cursor":""}`))
	return op
}
func relationshipsContract(path, query *jsonschema.Schema) openapi.Operation {
	op := operation("listTaskRelationships", "List all prerequisites or dependent tasks", nil, path, query)
	delete(op.Responses, "409")
	op.Responses["200"] = response("Choose prerequisites (default) for everything this task waits on, or dependents for every task waiting on it. Includes links through other tasks. Each task appears once, with the fewest dependency steps between them. Pages default to 20, maximum 100, ordered by steps then code.", pageSchema(relationSchema()), json.RawMessage(`{"items":[{"id":"PRJ-0001-T-0001","name":"Pour slab","status":"not_started","steps":1}],"next_cursor":""}`))
	return op
}

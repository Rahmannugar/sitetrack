package notifications

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"sitetrack/internal/infra/openapi"
)

var example = json.RawMessage(`{"id":"019f2aa0-7000-7000-8000-000000000004","project_id":"PRJ-0001","type":"project.invitation","title":"Project invitation","message":"You have been invited to PRJ-0001.","read_at":null,"created_at":"2026-11-10T12:00:00Z","invitation":{"id":"019f2aa0-7000-7000-8000-000000000003","status":"pending","can_respond":true}}`)

func schema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"id", "project_id", "type", "title", "message", "read_at", "created_at"}, Properties: map[string]*jsonschema.Schema{
		"id": {Type: "string", Format: "uuid"}, "project_id": {Type: "string"}, "type": {Type: "string"}, "title": {Type: "string"}, "message": {Type: "string"}, "read_at": {Types: []string{"string", "null"}, Format: "date-time"}, "created_at": {Type: "string", Format: "date-time"}, "invitation": {Type: "object", Required: []string{"id", "status", "can_respond"}, Properties: map[string]*jsonschema.Schema{"id": {Type: "string", Format: "uuid"}, "status": {Type: "string", Pattern: "^(pending|accepted|rejected|withdrawn)$"}, "can_respond": {Type: "boolean"}}},
	}}
}
func response(description string, schema *jsonschema.Schema, value json.RawMessage) openapi.Response {
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{"application/json": {Schema: schema, Examples: map[string]openapi.Example{"example": {Summary: description, Value: value}}}}}
}
func operation(id, summary string) openapi.Operation {
	errors := map[string]openapi.Response{}
	for _, value := range []struct{ code, message string }{{"400", "invalid notification request"}, {"401", "authentication required"}, {"429", "rate limit exceeded"}, {"500", "internal server error"}, {"504", "request timed out"}} {
		raw, _ := json.Marshal(struct {
			Error string `json:"error"`
		}{value.message})
		errors[value.code] = response(value.message, &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, raw)
	}
	return openapi.Operation{OperationID: id, Summary: summary, Tags: []string{"Notifications"}, Security: []map[string][]string{{"bearerAuth": {}}}, Responses: errors}
}
func listContract(query *jsonschema.Schema) openapi.Operation {
	op := operation("listNotifications", "List your accessible notifications")
	op.Parameters = openapi.Parameters(query, "query")
	page := &jsonschema.Schema{Type: "object", Required: []string{"items", "next_cursor", "unread_count"}, Properties: map[string]*jsonschema.Schema{"items": {Type: "array", Items: schema()}, "next_cursor": {Type: "string"}, "unread_count": {Type: "integer"}}}
	op.Responses["200"] = response("Newest first, default 20, maximum 100. Continue with next_cursor. Unread count includes only accessible notifications. Invitation status is current, including before or after membership.", page, json.RawMessage(`{"items":[`+string(example)+`],"next_cursor":"","unread_count":1}`))
	return op
}
func unreadContract() openapi.Operation {
	op := operation("unreadNotifications", "Count your accessible unread notifications")
	op.Responses["200"] = response("Current unread count.", &jsonschema.Schema{Type: "object", Required: []string{"unread_count"}, Properties: map[string]*jsonschema.Schema{"unread_count": {Type: "integer"}}}, json.RawMessage(`{"unread_count":1}`))
	return op
}
func readContract(path *jsonschema.Schema, all bool) openapi.Operation {
	id, summary := "readNotification", "Mark your notification as read"
	if all {
		id, summary = "readAllNotifications", "Mark all accessible notifications as read"
	}
	op := operation(id, summary)
	if path != nil {
		op.Parameters = openapi.Parameters(path, "path")
		op.Responses["404"] = response("Notification is nonexistent, inaccessible or belongs to someone else.", &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, json.RawMessage(`{"error":"notification not found"}`))
	}
	op.Responses["204"] = openapi.Response{Description: "Read state persisted; SSE clients receive the updated state."}
	return op
}
func streamContract(header *jsonschema.Schema) openapi.Operation {
	op := operation("streamNotifications", "Stream your notification changes")
	op.Parameters = openapi.Parameters(header, "header")
	op.Responses["200"] = openapi.Response{Description: "Bearer-authenticated SSE. Without Last-Event-ID, snapshot contains the newest 20 and pagination cursor. Resume using the opaque SSE id as Last-Event-ID. notifications events contain up to 20 current records and unread_count; clients upsert by notification id. state events refresh unread_count at least every five seconds. Project access and the session are rechecked before each batch; session_expired closes the stream. Streams close after one hour or process shutdown. Two streams per user, 5000 total. Use the paginated list for older history.", Content: map[string]openapi.MediaType{"text/event-stream": {Schema: &jsonschema.Schema{Type: "string"}, Examples: map[string]openapi.Example{"state": {Summary: "Current unread state and opaque replay cursor", Value: json.RawMessage(`"id: MQ\nevent: state\ndata: {\"unread_count\":1}\n\n"`)}}}}}
	op.Responses["503"] = response("Stream capacity reached.", &jsonschema.Schema{Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}}}, json.RawMessage(`{"error":"notification stream capacity reached"}`))
	return op
}

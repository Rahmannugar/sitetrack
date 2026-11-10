package projects

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"sitetrack/internal/infra/openapi"
)

var invitationExample = json.RawMessage(`{"id":"019f2aa0-7000-7000-8000-000000000003","project_id":"PRJ-0001","inviter_id":"019f2aa0-7000-7000-8000-000000000001","invitee_id":"019f2aa0-7000-7000-8000-000000000002","status":"pending","created_at":"2026-11-10T12:00:00Z","completed_at":null}`)

func invitationSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"id", "project_id", "inviter_id", "invitee_id", "status", "created_at", "completed_at"}, Properties: map[string]*jsonschema.Schema{
		"id": {Type: "string", Format: "uuid"}, "project_id": {Type: "string"}, "inviter_id": {Type: "string", Format: "uuid"}, "invitee_id": {Type: "string", Format: "uuid"}, "status": {Type: "string", Pattern: "^(pending|accepted|rejected|withdrawn)$"}, "created_at": {Type: "string", Format: "date-time"}, "completed_at": {Types: []string{"string", "null"}, Format: "date-time"},
	}}
}
func invitationContract(id, summary string, body, path *jsonschema.Schema, admin bool) openapi.Operation {
	op := projectOperation(id, summary, body, path, admin)
	op.Responses["404"] = response("The project, invitation or invitee does not exist or is inaccessible.", errorSchema(), json.RawMessage(`{"error":"invitation not found"}`))
	op.Responses["409"] = response("Only pending invitations can be answered or withdrawn; existing members and duplicate pending invitations are rejected.", errorSchema(), json.RawMessage(`{"error":"invitation is no longer pending or user already belongs to the project"}`))
	return op
}
func inviteContract(body, path *jsonschema.Schema) openapi.Operation {
	op := invitationContract("inviteProjectUser", "Invite a registered user", body, path, true)
	op.Responses["201"] = response("Invitation and notification job commit together. Membership begins only after acceptance. Invitations remain pending until answered or withdrawn.", invitationSchema(), invitationExample)
	return op
}
func invitationsContract(query, path *jsonschema.Schema) openapi.Operation {
	op := invitationContract("listProjectInvitations", "List project invitations", nil, path, false)
	delete(op.Responses, "409")
	op.Parameters = append(op.Parameters, openapi.Parameters(query, "query")...)
	op.Responses["200"] = response("Newest first with stable cursor pagination. Default status pending; use all for history. Default limit 20, maximum 100. Keep the same project and status when continuing.", pageSchema(invitationSchema()), json.RawMessage(`{"items":[`+string(invitationExample)+`],"next_cursor":""}`))
	return op
}
func withdrawContract(path *jsonschema.Schema) openapi.Operation {
	op := invitationContract("withdrawProjectInvitation", "Withdraw a pending invitation", nil, path, true)
	op.Responses["200"] = response("Any current project admin can withdraw a pending invitation. The invitee's notification reflects the changed status.", invitationSchema(), json.RawMessage(`{"id":"019f2aa0-7000-7000-8000-000000000003","project_id":"PRJ-0001","inviter_id":"019f2aa0-7000-7000-8000-000000000001","invitee_id":"019f2aa0-7000-7000-8000-000000000002","status":"withdrawn","created_at":"2026-11-10T12:00:00Z","completed_at":"2026-11-10T12:01:00Z"}`))
	return op
}
func respondContract(body, path *jsonschema.Schema) openapi.Operation {
	op := invitationContract("respondToProjectInvitation", "Accept or reject your invitation", body, path, false)
	op.Responses["200"] = response("Only the invitee may respond. Acceptance and non-admin membership commit atomically. A rejected invitation does not add membership.", invitationSchema(), json.RawMessage(`{"id":"019f2aa0-7000-7000-8000-000000000003","project_id":"PRJ-0001","inviter_id":"019f2aa0-7000-7000-8000-000000000001","invitee_id":"019f2aa0-7000-7000-8000-000000000002","status":"accepted","created_at":"2026-11-10T12:00:00Z","completed_at":"2026-11-10T12:01:00Z"}`))
	return op
}

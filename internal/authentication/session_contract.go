package authentication

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"

	"sitetrack/internal/infra/openapi"
)

func loginContract(schema *jsonschema.Schema) openapi.Operation {
	responses := sessionErrors("invalid email or password")
	responses["200"] = tokenContract("A new session replaces the account's previous session. Email verification is required.")
	return accountContract("login", "Log in and replace the previous session", schema, responses)
}

func refreshContract(schema *jsonschema.Schema) openapi.Operation {
	responses := sessionErrors("authentication required")
	responses["200"] = tokenContract("Rotate the single-use refresh token and extend the same session by seven days, up to thirty days from login. Reuse revokes the session; clients must serialize refresh requests.")
	return accountContract("refresh", "Renew JWT access and rotate the refresh token", schema, responses)
}

func profileContract() openapi.Operation {
	responses := sessionErrors("authentication required")
	delete(responses, "400")
	responses["200"] = openapi.Response{Description: "The current account profile and current app role.", Content: map[string]openapi.MediaType{
		"application/json": {Schema: profileSchema(), Examples: map[string]openapi.Example{"profile": {Summary: "Verified account", Value: profileExample}}},
	}}
	operation := accountContract("profile", "Read your profile", nil, responses)
	operation.Security = []map[string][]string{{"bearerAuth": {}}}
	return operation
}

func logoutContract() openapi.Operation {
	responses := sessionErrors("authentication required")
	delete(responses, "400")
	responses["200"] = messageContract("The current session and all its refresh tokens stop working immediately.", `{"message":"Logged out."}`)
	operation := accountContract("logout", "Revoke your current session", nil, responses)
	operation.Security = []map[string][]string{{"bearerAuth": {}}}
	return operation
}

func sessionErrors(message string) map[string]openapi.Response {
	responses := accountErrorContracts()
	responses["400"] = errorContract("Invalid request body.", map[string]string{"body": "invalid request body"})
	responses["401"] = errorContract("Authentication was not accepted.", map[string]string{"unauthorized": message})
	return responses
}

func profileSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"id", "name", "email", "role", "email_verified"}, Properties: map[string]*jsonschema.Schema{
		"id": {Type: "string", Format: "uuid"}, "name": {Type: "string"}, "email": {Type: "string", Format: "email"},
		"role": {Type: "string", Pattern: "^(user|company_store_manager|superadmin)$"}, "email_verified": {Type: "boolean"},
	}}
}

var profileExample = json.RawMessage(`{"id":"019f2aa0-7000-7000-8000-000000000001","name":"Site Engineer","email":"engineer@example.com","role":"user","email_verified":true}`)

func tokenContract(description string) openapi.Response {
	refreshLength := 43
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{
		"application/json": {Schema: &jsonschema.Schema{Type: "object", Required: []string{"access_token", "access_token_expires_at", "refresh_token", "refresh_token_expires_at", "token_type", "user"}, Properties: map[string]*jsonschema.Schema{
			"access_token": {Type: "string"}, "access_token_expires_at": {Type: "string", Format: "date-time"},
			"refresh_token": {Type: "string", MinLength: &refreshLength, MaxLength: &refreshLength}, "refresh_token_expires_at": {Type: "string", Format: "date-time"},
			"token_type": {Type: "string", Pattern: "^Bearer$"}, "user": profileSchema(),
		}}, Examples: map[string]openapi.Example{"tokens": {Summary: "JWT access and opaque refresh token", Value: json.RawMessage(`{
"access_token":"eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIwMTlmMmFhMC03MDAwLTcwMDAtODAwMC0wMDAwMDAwMDAwMDEifQ.c2lnbmF0dXJl",
"access_token_expires_at":"2026-11-10T12:15:00Z",
"refresh_token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
"refresh_token_expires_at":"2026-11-17T12:00:00Z",
"token_type":"Bearer",
"user":{"id":"019f2aa0-7000-7000-8000-000000000001","name":"Site Engineer","email":"engineer@example.com","role":"user","email_verified":true}
}`)}}},
	}}
}

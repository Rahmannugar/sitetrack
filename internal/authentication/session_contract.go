package authentication

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"

	"sitetrack/internal/infra/openapi"
)

func loginContract(schema *jsonschema.Schema) openapi.Operation {
	responses := sessionErrors("invalid email or password")
	responses["403"] = errorContract("Email verification required.", map[string]string{"unverified": "verify your email before logging in"})
	responses["200"] = tokenContract("Logged in. Any previous session is logged out.")
	return accountContract("login", "Log in", schema, responses)
}

func refreshContract(schema *jsonschema.Schema) openapi.Operation {
	responses := sessionErrors("authentication required")
	responses["200"] = tokenContract("Session renewed for seven days, up to thirty days from login. Each refresh token works once; reusing it logs you out.")
	return accountContract("refresh", "Refresh your session", schema, responses)
}

func profileContract() openapi.Operation {
	responses := sessionErrors("authentication required")
	delete(responses, "400")
	responses["200"] = openapi.Response{Description: "Your profile and app role.", Content: map[string]openapi.MediaType{
		"application/json": {Schema: profileSchema(), Examples: map[string]openapi.Example{"profile": {Summary: "Verified account", Value: profileExample}}},
	}}
	operation := accountContract("profile", "Read your profile", nil, responses)
	operation.Security = []map[string][]string{{"bearerAuth": {}}}
	return operation
}

func logoutContract() openapi.Operation {
	responses := sessionErrors("authentication required")
	delete(responses, "400")
	responses["200"] = messageContract("Logged out.", `{"message":"Logged out."}`)
	operation := accountContract("logout", "Log out", nil, responses)
	operation.Security = []map[string][]string{{"bearerAuth": {}}}
	return operation
}

func sessionErrors(message string) map[string]openapi.Response {
	responses := accountErrorContracts()
	responses["400"] = errorContract("Invalid request body.", map[string]string{"body": "invalid request body"})
	responses["401"] = errorContract("Log in to continue.", map[string]string{"unauthorized": message})
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
		}}, Examples: map[string]openapi.Example{"tokens": {Summary: "Login tokens", Value: json.RawMessage(`{
"access_token":"eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIwMTlmMmFhMC03MDAwLTcwMDAtODAwMC0wMDAwMDAwMDAwMDEifQ.c2lnbmF0dXJl",
"access_token_expires_at":"2026-11-10T12:15:00Z",
"refresh_token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
"refresh_token_expires_at":"2026-11-17T12:00:00Z",
"token_type":"Bearer",
"user":{"id":"019f2aa0-7000-7000-8000-000000000001","name":"Site Engineer","email":"engineer@example.com","role":"user","email_verified":true}
}`)}}},
	}}
}

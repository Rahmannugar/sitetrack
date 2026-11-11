package authentication

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"

	"sitetrack/internal/infra/openapi"
)

func signupContract(schema *jsonschema.Schema) openapi.Operation {
	responses := accountErrorContracts()
	responses["201"] = messageContract("Account created. Check your email for the verification code.", `{"message":"Check your email for a verification code."}`)
	responses["409"] = errorContract("Email already registered.", map[string]string{"registered": "email already registered"})
	responses["400"] = errorContract("Invalid registration request.", map[string]string{
		"body": "invalid request body", "details": "invalid account details",
	})
	return accountContract("signup", "Create an account", schema, responses)
}

func verifyContract(schema *jsonschema.Schema) openapi.Operation {
	responses := accountErrorContracts()
	responses["200"] = messageContract("The email address is verified and the code is consumed.", `{"message":"Email verified."}`)
	responses["400"] = errorContract("Invalid request or verification code.", map[string]string{
		"body": "invalid request body", "code": "invalid or expired verification code",
	})
	return accountContract("verifyEmail", "Verify an email address using its OTP", schema, responses)
}

func resendContract(schema *jsonschema.Schema) openapi.Operation {
	responses := accountErrorContracts()
	responses["202"] = messageContract("Check your email for a verification code.", `{"message":"Check your email for a verification code."}`)
	responses["400"] = errorContract("Invalid email verification request.", map[string]string{"body": "invalid request body"})
	return accountContract("resendVerification", "Request another email verification code", schema, responses)
}

func forgotPasswordContract(schema *jsonschema.Schema) openapi.Operation {
	responses := accountErrorContracts()
	responses["202"] = messageContract("Check your email for a password reset code.", `{"message":"Check your email for a password reset code."}`)
	responses["400"] = errorContract("Invalid email.", map[string]string{"body": "invalid request body", "details": "invalid account details"})
	return accountContract("forgotPassword", "Request a password reset code", schema, responses)
}

func resetPasswordContract(schema *jsonschema.Schema) openapi.Operation {
	responses := accountErrorContracts()
	responses["200"] = messageContract("Password changed and you are logged out. Log in with your new password.", `{"message":"Password reset. Log in with your new password."}`)
	responses["400"] = errorContract("Invalid password or reset code.", map[string]string{"body": "invalid request body", "details": "invalid account details", "code": "invalid or expired password reset code"})
	return accountContract("resetPassword", "Reset your password using the OTP", schema, responses)
}

func accountContract(id, summary string, schema *jsonschema.Schema, responses map[string]openapi.Response) openapi.Operation {
	operation := openapi.Operation{OperationID: id, Summary: summary, Tags: []string{"Authentication"}, Responses: responses}
	if schema != nil {
		operation.RequestBody = &openapi.RequestBody{Required: true, Content: map[string]openapi.MediaType{"application/json": {Schema: schema}}}
	}
	return operation
}

func messageContract(description, example string) openapi.Response {
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{
		"application/json": {Schema: &jsonschema.Schema{
			Type: "object", Required: []string{"message"}, Properties: map[string]*jsonschema.Schema{"message": {Type: "string"}},
		}, Examples: map[string]openapi.Example{"result": {Summary: description, Value: json.RawMessage(example)}}},
	}}
}

func errorContract(description string, messages map[string]string) openapi.Response {
	examples := make(map[string]openapi.Example, len(messages))
	for name, message := range messages {
		value, _ := json.Marshal(errorResponse{Error: message})
		examples[name] = openapi.Example{Summary: message, Value: value}
	}
	return openapi.Response{Description: description, Content: map[string]openapi.MediaType{
		"application/json": {Schema: &jsonschema.Schema{
			Type: "object", Required: []string{"error"}, Properties: map[string]*jsonschema.Schema{"error": {Type: "string"}},
		}, Examples: examples},
	}}
}

func accountErrorContracts() map[string]openapi.Response {
	return map[string]openapi.Response{
		"429": errorContract("The endpoint's rate limit is exhausted.", map[string]string{"limited": "rate limit exceeded"}),
		"500": errorContract("The operation could not complete.", map[string]string{"failure": "internal server error"}),
		"504": errorContract("The operation timeout expired.", map[string]string{"timeout": "request timed out"}),
	}
}

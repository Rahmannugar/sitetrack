package request_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sitetrack/internal/authentication"
	"sitetrack/internal/infra/request"
)

func TestGeneratedSignupContractMatchesRuntimeValidation(t *testing.T) {
	body, err := request.NewBody[authentication.SignupInput]()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := body.Schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/signup", func(c *gin.Context) {
		if _, err := body.Decode(c); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusNoContent)
	})
	for _, test := range []struct {
		body  string
		valid bool
	}{
		{`{"name":"Engineer","email":"person@example.test","password":"secure-password-123","role":"user"}`, true},
		{`{"name":"Engineer","email":"person@example.test","password":"short","role":"user"}`, false},
		{`{"name":"Engineer","email":"person@example.test","password":"secure-password-123","role":"owner"}`, false},
		{`{"name":"Engineer","email":"person@example.test","password":"secure-password-123"}`, false},
		{`{"name":"Engineer","email":"person@example.test","password":"secure-password-123","role":"user","extra":"private"}`, false},
	} {
		var value map[string]string
		if err := json.Unmarshal([]byte(test.body), &value); err != nil {
			t.Fatal(err)
		}
		if valid := schema.Validate(value) == nil; valid != test.valid {
			t.Fatalf("generated request contract disagrees with expected validation: %s", test.body)
		}
		r := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(test.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if valid := w.Code == http.StatusNoContent; valid != test.valid {
			t.Fatal("runtime validation disagrees with its generated contract")
		}
	}
}

func TestVerificationBodyRejectsMalformedAndTrailingInput(t *testing.T) {
	body, err := request.NewBody[authentication.VerificationInput]()
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"email":"person@example.test","code":"12.345"}`,
		`{"email":"person@example.test","code":"123456"} {}`,
		strings.Repeat("x", 33<<10),
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(input))
		c.Request.Header.Set("Content-Type", "application/json")
		if _, err := body.Decode(c); err == nil {
			t.Fatal("malformed verification body was accepted")
		}
	}
}

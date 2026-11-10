package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sitetrack/internal/config"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/telemetry"
)

type healthDatabase struct {
	err error
}

func (db *healthDatabase) Ping(context.Context) error { return db.err }

func testAPIRouter(t *testing.T, database *healthDatabase) *gin.Engine {
	t.Helper()
	monitoring, err := telemetry.New(context.Background(), config.Telemetry{}, "sitetrack-api", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	router, err := newRouter(database, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func servedDocument(t *testing.T, router *gin.Engine) openapi.Document {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, openapi.SpecPath, nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("OpenAPI response: %d %s", w.Code, w.Body.String())
	}
	var document openapi.Document
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestOpenAPICoversMountedRoutes(t *testing.T) {
	router := testAPIRouter(t, &healthDatabase{})
	document := servedDocument(t, router)
	if document.OpenAPI != "3.1.1" || document.Info.Title != "SiteTrack API" || document.Info.Version == "" {
		t.Fatal("missing API identity or unsupported OpenAPI version")
	}
	if len(document.Servers) != 1 || document.Servers[0].URL != "/" {
		t.Fatal("the interactive reference must use the current API origin")
	}
	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet && (route.Path == openapi.DocsPath || route.Path == openapi.SpecPath) {
			continue
		}
		method := strings.ToLower(route.Method)
		operation, found := document.Paths[route.Path][method]
		if !found || operation.OperationID == "" {
			t.Errorf("undocumented route: %s %s", route.Method, route.Path)
		}
		registered[method+" "+route.Path] = true
	}
	ids := make(map[string]bool)
	for path, methods := range document.Paths {
		for method, operation := range methods {
			if !registered[method+" "+path] {
				t.Errorf("documented route is not mounted: %s %s", method, path)
			}
			if ids[operation.OperationID] {
				t.Errorf("duplicate operation ID: %s", operation.OperationID)
			}
			ids[operation.OperationID] = true
		}
	}
}

func TestHealthResponsesMatchSchemasAndExamples(t *testing.T) {
	database := &healthDatabase{}
	router := testAPIRouter(t, database)
	document := servedDocument(t, router)
	for _, test := range []struct {
		path   string
		status int
		err    error
	}{
		{path: "/health/live", status: http.StatusOK},
		{path: "/health/ready", status: http.StatusOK},
		{path: "/health/ready", status: http.StatusServiceUnavailable, err: errors.New("private database failure")},
		{path: "/health/live", status: http.StatusOK, err: errors.New("private database failure")},
	} {
		t.Run(test.path+"/"+strconv.Itoa(test.status), func(t *testing.T) {
			database.err = test.err
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, test.path, nil))
			if w.Code != test.status {
				t.Fatalf("status = %d, want %d", w.Code, test.status)
			}
			response := document.Paths[test.path]["get"].Responses[strconv.Itoa(w.Code)]
			media := response.Content["application/json"]
			if response.Description == "" || media.Schema == nil || len(media.Examples) == 0 {
				t.Fatal("response contract lacks its description, schema or operation-level example")
			}
			schema, err := media.Schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &actual); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(actual); err != nil {
				t.Fatalf("runtime response violates its schema: %v", err)
			}
			matched := false
			for _, example := range media.Examples {
				var value map[string]string
				if err := json.Unmarshal(example.Value, &value); err != nil {
					t.Fatal(err)
				}
				if err := schema.Validate(value); err != nil {
					t.Fatalf("example violates its response schema: %v", err)
				}
				matched = matched || reflect.DeepEqual(actual, value)
			}
			if !matched {
				t.Fatal("runtime response does not match its documented example")
			}
		})
	}
}

func TestScalarLoadsTheSameOriginSpecification(t *testing.T) {
	router := testAPIRouter(t, &healthDatabase{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, openapi.DocsPath, nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatal("API reference is not served as HTML")
	}
	for _, expected := range []string{
		"Scalar.createApiReference", "url: '/api/openapi.json'", "@scalar/api-reference@1.76.4/", "persistAuth: false",
	} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Errorf("API reference lacks %q", expected)
		}
	}
	if strings.Contains(w.Body.String(), "proxy.scalar.com") {
		t.Fatal("same-origin API requests must not go through a hosted proxy")
	}
}

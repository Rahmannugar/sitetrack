package materials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rahmannugar/authlier/password"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"sitetrack/internal/authentication"
	"sitetrack/internal/config"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/telemetry"
)

type fixture struct {
	s          *Service
	accounts   *authentication.Service
	users      []uuid.UUID
	emails     []string
	categories []uuid.UUID
	prefix     string
	spans      *tracetest.InMemoryExporter
	metrics    *sdkmetric.ManualReader
	logs       *bytes.Buffer
}

func setup(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	logs := &bytes.Buffer{}
	monitoring, err := telemetry.New(ctx, config.Telemetry{}, "catalogue-test", logs)
	if err != nil {
		t.Fatal(err)
	}
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	monitoring.Tracer = provider.Tracer("catalogue-test")
	monitoring.Meter = meterProvider.Meter("catalogue-test")
	pool, err := database.Open(ctx, url, 10, database.WithTracer(monitoring.PostgresTracer()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := authentication.NewService(pool, config.Authentication{OTPSecret: []byte("12345678901234567890123456789012"), OutboxKey: []byte("abcdefghijklmnopqrstuvxyz1234567"), JWTSeed: []byte("12345678901234567890123456789012")})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: service, accounts: accounts, prefix: uuid.NewString(), spans: exporter, metrics: reader, logs: logs}
	hash, err := password.Hash("A-secure-password-123")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"user", "company_store_manager", "superadmin"} {
		id := uuid.Must(uuid.NewV7())
		email := id.String() + "@example.test"
		if _, err := pool.Exec(ctx, "INSERT INTO sitetrack.users(id,name,email,password_hash,role,email_verified) VALUES($1,'Catalogue Engineer',$2,$3,$4,true)", id, email, hash, role); err != nil {
			t.Fatal(err)
		}
		f.users = append(f.users, id)
		f.emails = append(f.emails, email)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, sql := range []string{"DELETE FROM sitetrack.materials WHERE category_id=ANY($1)", "DELETE FROM sitetrack.material_categories WHERE id=ANY($1)"} {
			if _, err := pool.Exec(ctx, sql, f.categories); err != nil {
				t.Error(err)
			}
		}
		if _, err := pool.Exec(ctx, "DELETE FROM sitetrack.users WHERE id=ANY($1)", f.users); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *fixture) category(t *testing.T, value string) Category {
	t.Helper()
	result, err := f.s.CreateCategory(context.Background(), f.users[1], CategoryInput{Name: f.prefix + value})
	if err != nil {
		t.Fatal(err)
	}
	f.categories = append(f.categories, result.ID)
	return result
}
func (f *fixture) material(t *testing.T, category uuid.UUID, value string) Material {
	t.Helper()
	result, err := f.s.Create(context.Background(), f.users[1], Input{Name: f.prefix + value, CategoryID: category.String(), Unit: "bag"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCatalogueRulesRemovalAndConcurrentChanges(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	category := f.category(t, " Cement")
	item := f.material(t, category.ID, " Cement 50 kg")
	if item.ID.Version() != 7 || category.ID.Version() != 7 || item.CategoryName != category.Name {
		t.Fatal("catalogue identity/category mapping is wrong")
	}
	if _, err := f.s.CreateCategory(ctx, f.users[1], CategoryInput{Name: strings.ToUpper(category.Name)}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate category accepted", err)
	}
	if _, err := f.s.Create(ctx, f.users[1], Input{Name: strings.ToUpper(item.Name), CategoryID: category.ID.String(), Unit: "bag"}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate material accepted", err)
	}
	if err := f.s.RemoveCategory(ctx, f.users[1], category.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("in-use category removed", err)
	}
	renamed, err := f.s.UpdateCategory(ctx, f.users[1], category.ID, CategoryInput{Name: category.Name + " renamed"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.s.Get(ctx, f.users[0], item.ID)
	if err != nil || current.CategoryName != renamed.Name {
		t.Fatal("category name is stale", err)
	}
	next := f.category(t, " Steel")
	updated, err := f.s.Update(ctx, f.users[2], item.ID, Input{Name: item.Name + " updated", CategoryID: next.ID.String(), Unit: "kg"})
	if err != nil || updated.ID != item.ID || updated.Unit != "kg" {
		t.Fatal("material edit failed", err)
	}
	if err := f.s.RemoveCategory(ctx, f.users[1], category.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Create(ctx, f.users[1], Input{Name: "Invalid Material", CategoryID: category.ID.String(), Unit: "bag"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed category reused", err)
	}
	if err := f.s.Remove(ctx, f.users[1], item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.users[0], item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed material visible", err)
	}
	var retained bool
	if err := f.s.pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM sitetrack.materials WHERE id=$1", item.ID).Scan(&retained); err != nil || !retained {
		t.Fatal("material history lost", err)
	}
	if err := f.s.RemoveCategory(ctx, f.users[1], next.ID); err != nil {
		t.Fatal(err)
	}
	raceCategory := f.category(t, " Race")
	start := make(chan struct{})
	var group sync.WaitGroup
	var createErr, removeErr error
	group.Go(func() {
		<-start
		_, createErr = f.s.Create(ctx, f.users[1], Input{Name: f.prefix + " Race Item", CategoryID: raceCategory.ID.String(), Unit: "piece"})
	})
	group.Go(func() { <-start; removeErr = f.s.RemoveCategory(ctx, f.users[2], raceCategory.ID) })
	close(start)
	group.Wait()
	if !((createErr == nil && errors.Is(removeErr, ErrConflict)) || (errors.Is(createErr, ErrNotFound) && removeErr == nil)) {
		t.Fatal("category creation/removal race inconsistent", createErr, removeErr)
	}
}

func TestCataloguePaginationFiltersAndLiteralSearch(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	category := f.category(t, " Items")
	for i := range 23 {
		f.material(t, category.ID, fmt.Sprintf(" Material %02d", i))
	}
	f.material(t, category.ID, " Percent%_\\ Item")
	input := ListInput{Search: f.prefix, Limit: 20, CategoryID: category.ID.String()}
	first, err := f.s.List(ctx, f.users[0], input)
	if err != nil || len(first.Items) != 20 || first.NextCursor == "" {
		t.Fatal("first material page", err)
	}
	input.Cursor = first.NextCursor
	second, err := f.s.List(ctx, f.users[0], input)
	if err != nil || len(second.Items) != 4 || second.NextCursor != "" {
		t.Fatal("material continuation", err)
	}
	seen := map[uuid.UUID]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.ID] {
			t.Fatal("duplicate page item")
		}
		seen[item.ID] = true
	}
	input.Search = "different"
	if _, err := f.s.List(ctx, f.users[0], input); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor accepted changed filters", err)
	}
	matches, err := f.s.List(ctx, f.users[0], ListInput{Search: `%_\`, Limit: 100})
	if err != nil || len(matches.Items) != 1 {
		t.Fatal("literal search treated wildcards as syntax", err)
	}
	for _, limit := range []int{0, 101} {
		if _, err := f.s.List(ctx, f.users[0], ListInput{Limit: limit}); !errors.Is(err, ErrInvalid) {
			t.Fatal("unbounded material page accepted")
		}
	}
	for _, suffix := range []string{"A", "B"} {
		if _, err := f.s.Create(ctx, f.users[1], Input{Name: strings.Repeat("<", 140) + suffix, CategoryID: category.ID.String(), Unit: "bag"}); err != nil {
			t.Fatal(err)
		}
	}
	longInput := ListInput{Search: strings.Repeat("<", 140), CategoryID: category.ID.String(), Limit: 1}
	longPage, err := f.s.List(ctx, f.users[0], longInput)
	if err != nil || len(longPage.Items) != 1 || longPage.NextCursor == "" {
		t.Fatal("long-name cursor missing", err)
	}
	longInput.Cursor = longPage.NextCursor
	if rest, err := f.s.List(ctx, f.users[0], longInput); err != nil || len(rest.Items) != 1 || rest.NextCursor != "" {
		t.Fatal("long-name cursor rejected", err)
	}
	for i := range 21 {
		f.category(t, fmt.Sprintf(" Page %02d", i))
	}
	categories, err := f.s.Categories(ctx, f.users[0], CategoryListInput{Search: f.prefix, Limit: 20})
	if err != nil || len(categories.Items) != 20 || categories.NextCursor == "" {
		t.Fatal("category pagination failed", err)
	}
	if _, err := f.s.List(ctx, f.users[0], ListInput{Limit: 20, Cursor: categories.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatal("category cursor accepted for materials", err)
	}
	rest, err := f.s.Categories(ctx, f.users[0], CategoryListInput{Search: f.prefix, Limit: 20, Cursor: categories.NextCursor})
	if err != nil || len(rest.Items) != 2 {
		t.Fatal("category continuation failed", err)
	}
}

func TestCatalogueHTTPPermissionsContractsAndTelemetry(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	category := f.category(t, " HTTP")
	item := f.material(t, category.ID, " HTTP Item")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := openapi.New()
	if err := Register(router.Group(""), f.s, f.accounts, registry, false, slog.New(slog.NewJSONHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	tokens := make([]string, 3)
	for i := range tokens {
		pair, err := f.accounts.Login(ctx, authentication.LoginInput{Email: f.emails[i], Password: "A-secure-password-123"})
		if err != nil {
			t.Fatal(err)
		}
		tokens[i] = pair.AccessToken
	}
	call := func(method, path, token string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		return response
	}
	input := Input{Name: item.Name, CategoryID: category.ID.String(), Unit: item.Unit}
	for _, entry := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/materials", input}, {"PUT", "/materials/" + item.ID.String(), input}, {"DELETE", "/materials/" + item.ID.String(), nil},
		{"POST", "/material-categories", CategoryInput{Name: "Blocked"}}, {"PUT", "/material-categories/" + category.ID.String(), CategoryInput{Name: "Blocked"}}, {"DELETE", "/material-categories/" + category.ID.String(), nil},
	} {
		call(entry.method, entry.path, tokens[0], entry.body, 403)
	}
	for _, path := range []string{"/materials", "/materials/" + item.ID.String(), "/material-categories", "/material-categories/" + category.ID.String()} {
		call("GET", path, "", nil, 401)
		call("GET", path, tokens[0], nil, 200)
	}
	for _, path := range []string{"/materials?limit=101", "/materials?cursor=bad", "/materials?category_id=bad", "/materials/bad"} {
		call("GET", path, tokens[0], nil, 400)
	}
	call("POST", "/materials", tokens[1], Input{Name: "  ", CategoryID: category.ID.String(), Unit: "bag"}, 400)
	call("POST", "/materials", tokens[1], Input{Name: "Valid Material", CategoryID: uuid.NewString(), Unit: "bag"}, 404)
	call("POST", "/materials", tokens[1], input, 409)
	if _, err := f.s.pool.Exec(ctx, "UPDATE sitetrack.users SET role='user' WHERE id=$1", f.users[1]); err != nil {
		t.Fatal(err)
	}
	call("PUT", "/materials/"+item.ID.String(), tokens[1], input, 403)
	input.Name += " updated"
	call("PUT", "/materials/"+item.ID.String(), tokens[2], input, 200)
	call("DELETE", "/materials/"+item.ID.String(), tokens[2], nil, 204)
	call("DELETE", "/material-categories/"+category.ID.String(), tokens[2], nil, 204)
	call("GET", "/materials/"+item.ID.String(), tokens[0], nil, 404)
	call("GET", "/material-categories/"+category.ID.String(), tokens[0], nil, 404)
	raw, err := registry.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var document openapi.Document
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Paths) != 4 {
		t.Fatal("catalogue routes missing")
	}
	for _, methods := range document.Paths {
		for _, operation := range methods {
			for _, response := range operation.Responses {
				for _, media := range response.Content {
					schema, err := media.Schema.Resolve(nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, example := range media.Examples {
						var value any
						if err := json.Unmarshal(example.Value, &value); err != nil {
							t.Fatal(err)
						}
						if err := schema.Validate(value); err != nil {
							t.Fatal("invalid catalogue example", operation.OperationID, err)
						}
					}
				}
			}
		}
	}
	found := false
	for _, span := range f.spans.GetSpans() {
		if span.Name == "UpdateMaterial" {
			for _, parent := range f.spans.GetSpans() {
				if parent.Name == "materials.update" && span.Parent.SpanID() == parent.SpanContext.SpanID() {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("database span not parented to catalogue operation")
	}
	var measured metricdata.ResourceMetrics
	if err := f.metrics.Collect(ctx, &measured); err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]bool{}
	for _, scope := range measured.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "catalogue.operations" {
				if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
					for _, point := range sum.DataPoints {
						if point.Attributes.Len() != 2 {
							t.Fatal("unbounded catalogue metric labels")
						}
						for _, a := range point.Attributes.ToSlice() {
							if string(a.Key) == "outcome" {
								outcomes[a.Value.AsString()] = true
							}
						}
					}
				}
			}
		}
	}
	if !outcomes["success"] || !outcomes["rejected"] {
		t.Fatal("catalogue outcomes not recorded", outcomes)
	}
	if !strings.Contains(f.logs.String(), "trace_id") || strings.Contains(f.logs.String(), item.Name) {
		t.Fatal("change logs lack correlation or include input values")
	}
}

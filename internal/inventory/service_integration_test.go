package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
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
	"sitetrack/internal/materials"
	"sitetrack/internal/projects"
)

type fixture struct {
	s          *Service
	catalogue  *materials.Service
	projects   *projects.Service
	accounts   *authentication.Service
	monitoring *telemetry.Runtime
	users      []uuid.UUID
	emails     []string
	categories []uuid.UUID
	projectIDs []string
	prefix     string
	spans      *tracetest.InMemoryExporter
	metrics    *sdkmetric.ManualReader
	logs       *bytes.Buffer
}

func inventoryTest(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	logs := &bytes.Buffer{}
	monitoring, err := telemetry.New(ctx, config.Telemetry{}, "inventory-test", logs)
	if err != nil {
		t.Fatal(err)
	}
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = mp.Shutdown(context.Background()) })
	monitoring.Tracer = tp.Tracer("inventory-test")
	monitoring.Meter = mp.Meter("inventory-test")
	pool, err := database.Open(ctx, url, 10, database.WithTracer(monitoring.PostgresTracer()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	stock, err := NewService(pool, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := materials.NewService(pool, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := authentication.NewService(pool, config.Authentication{OTPSecret: []byte("12345678901234567890123456789012"), OutboxKey: []byte("abcdefghijklmnopqrstuvxyz1234567"), JWTSeed: []byte("12345678901234567890123456789012")})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: stock, catalogue: catalogue, projects: projects.NewService(pool), accounts: accounts, monitoring: monitoring, prefix: uuid.NewString(), spans: spans, metrics: reader, logs: logs}
	hash, err := password.Hash("A-secure-password-123")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"user", "company_store_manager", "superadmin"} {
		id := uuid.Must(uuid.NewV7())
		email := id.String() + "@example.test"
		if _, err := pool.Exec(ctx, "INSERT INTO sitetrack.users(id,name,email,password_hash,role,email_verified) VALUES($1,'Inventory Engineer',$2,$3,$4,true)", id, email, hash, role); err != nil {
			t.Fatal(err)
		}
		f.users = append(f.users, id)
		f.emails = append(f.emails, email)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer rollback(tx)
		for _, sql := range []string{
			"DELETE FROM sitetrack.company_inventory WHERE material_id IN (SELECT id FROM sitetrack.materials WHERE category_id=ANY($1))",
			"DELETE FROM sitetrack.project_inventory WHERE material_id IN (SELECT id FROM sitetrack.materials WHERE category_id=ANY($1))",
			"DELETE FROM sitetrack.materials WHERE category_id=ANY($1)", "DELETE FROM sitetrack.material_categories WHERE id=ANY($1)",
		} {
			if _, err := tx.Exec(ctx, sql, f.categories); err != nil {
				t.Error(err)
				return
			}
		}
		for _, sql := range []string{"UPDATE sitetrack.projects SET deleted_at=now() WHERE id=ANY($1)", "DELETE FROM sitetrack.notifications WHERE project_id=ANY($1)", "DELETE FROM sitetrack.project_memberships WHERE project_id=ANY($1)", "DELETE FROM sitetrack.project_stores WHERE project_id=ANY($1)", "DELETE FROM sitetrack.projects WHERE id=ANY($1)"} {
			if _, err := tx.Exec(ctx, sql, f.projectIDs); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM sitetrack.users WHERE id=ANY($1)", f.users); err != nil {
			t.Error(err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *fixture) category(t *testing.T, suffix string) materials.Category {
	t.Helper()
	c, err := f.catalogue.CreateCategory(context.Background(), f.users[1], materials.CategoryInput{Name: f.prefix + suffix})
	if err != nil {
		t.Fatal(err)
	}
	f.categories = append(f.categories, c.ID)
	return c
}
func (f *fixture) material(t *testing.T, category uuid.UUID, suffix string) materials.Material {
	t.Helper()
	m, err := f.catalogue.Create(context.Background(), f.users[1], materials.Input{Name: f.prefix + suffix, CategoryID: category.String(), Unit: "bag"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func (f *fixture) project(t *testing.T) projects.Project {
	t.Helper()
	p, err := f.projects.Create(context.Background(), f.users[0], projects.Input{Name: "Inventory Site", StartDate: "2026-11-01", EndDate: "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	f.projectIDs = append(f.projectIDs, p.ID)
	return p
}
func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.s.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestStockExactConcurrentAdditionsReorderAndBounds(t *testing.T) {
	f := inventoryTest(t)
	ctx := context.Background()
	c := f.category(t, " Cement")
	m := f.material(t, c.ID, " Cement")
	before, err := f.s.CompanyItem(ctx, f.users[1], m.ID)
	if err != nil || before.Quantity != "0.000" || before.Status != "out_of_stock" {
		t.Fatal("missing zero-stock material", before, err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, err := f.s.AddStock(ctx, f.users[1], m.ID, QuantityInput{Quantity: "0.125"}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	stock, err := f.s.CompanyItem(ctx, f.users[2], m.ID)
	if err != nil || stock.Quantity != "1.000" || stock.Status != "available" {
		t.Fatal("lost or rounded concurrent stock", stock, err)
	}
	stock, err = f.s.SetReorderPoint(ctx, f.users[1], m.ID, ReorderInput{ReorderPoint: "1"})
	if err != nil || stock.Status != "low_stock" {
		t.Fatal("reorder threshold wrong", err)
	}
	stock, err = f.s.SetReorderPoint(ctx, f.users[2], m.ID, ReorderInput{ReorderPoint: "0"})
	if err != nil || stock.Status != "available" {
		t.Fatal("zero reorder point wrong", err)
	}
	for _, invalid := range []string{"0", "0.000", "-1", "NaN", "Infinity", "1e3", "1.0001", "9999999999999999", "", ".5", "1.", "01"} {
		if _, err := f.s.AddStock(ctx, f.users[1], m.ID, QuantityInput{Quantity: invalid}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid stock quantity accepted", invalid, err)
		}
	}
	maximum := f.material(t, c.ID, " Maximum")
	if _, err := f.s.AddStock(ctx, f.users[1], maximum.ID, QuantityInput{Quantity: "999999999999999.999"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddStock(ctx, f.users[1], maximum.ID, QuantityInput{Quantity: "0.001"}); !errors.Is(err, ErrConflict) {
		t.Fatal("overflow not rejected", err)
	}
	stock, err = f.s.CompanyItem(ctx, f.users[1], maximum.ID)
	if err != nil || stock.Quantity != "999999999999999.999" {
		t.Fatal("overflow changed stock", err)
	}
	for _, value := range []string{"-0.001", "NaN"} {
		if _, err := f.s.pool.Exec(ctx, "UPDATE sitetrack.company_inventory SET quantity=$2::text::numeric WHERE material_id=$1", m.ID, value); err == nil {
			t.Fatal("database accepted invalid stock", value)
		}
	}
}

func TestInventoryMaterialGuardsRollbackAndRemovalRace(t *testing.T) {
	f := inventoryTest(t)
	ctx := context.Background()
	c := f.category(t, " Guards")
	m := f.material(t, c.ID, " Stocked")
	if _, err := f.s.AddStock(ctx, f.users[1], m.ID, QuantityInput{Quantity: "2.001"}); err != nil {
		t.Fatal(err)
	}
	input := materials.Input{Name: m.Name, CategoryID: c.ID.String(), Unit: "kg"}
	if _, err := f.catalogue.Update(ctx, f.users[1], m.ID, input); !errors.Is(err, materials.ErrConflict) {
		t.Fatal("stocked unit changed", err)
	}
	if err := f.catalogue.Remove(ctx, f.users[1], m.ID); !errors.Is(err, materials.ErrConflict) {
		t.Fatal("stocked material removed", err)
	}
	f.exec(t, `CREATE FUNCTION sitetrack.reject_test_stock() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced stock failure'; END $$; CREATE TRIGGER reject_test_stock BEFORE UPDATE ON sitetrack.company_inventory FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_test_stock();`)
	drop := func() {
		f.exec(t, "DROP TRIGGER IF EXISTS reject_test_stock ON sitetrack.company_inventory; DROP FUNCTION IF EXISTS sitetrack.reject_test_stock()")
	}
	defer drop()
	if _, err := f.s.AddStock(ctx, f.users[1], m.ID, QuantityInput{Quantity: "1"}); err == nil {
		t.Fatal("stock failure ignored")
	}
	unchanged, err := f.s.CompanyItem(ctx, f.users[1], m.ID)
	if err != nil || unchanged.Quantity != "2.001" {
		t.Fatal("failed addition changed stock", err)
	}
	drop()
	empty := f.material(t, c.ID, " Empty")
	if _, err := f.s.SetReorderPoint(ctx, f.users[1], empty.ID, ReorderInput{ReorderPoint: "0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.catalogue.Update(ctx, f.users[1], empty.ID, materials.Input{Name: empty.Name, CategoryID: c.ID.String(), Unit: "kg"}); !errors.Is(err, materials.ErrConflict) {
		t.Fatal("inventory history did not protect unit", err)
	}
	if err := f.catalogue.Remove(ctx, f.users[1], empty.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddStock(ctx, f.users[1], empty.ID, QuantityInput{Quantity: "1"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed material restocked", err)
	}
	race := f.material(t, c.ID, " Race")
	start := make(chan struct{})
	var group sync.WaitGroup
	var addErr, removeErr error
	group.Go(func() { <-start; _, addErr = f.s.AddStock(ctx, f.users[1], race.ID, QuantityInput{Quantity: "0.001"}) })
	group.Go(func() { <-start; removeErr = f.catalogue.Remove(ctx, f.users[1], race.ID) })
	close(start)
	group.Wait()
	if !((addErr == nil && errors.Is(removeErr, materials.ErrConflict)) || (errors.Is(addErr, ErrNotFound) && removeErr == nil)) {
		t.Fatal("stock/removal race violated material state", addErr, removeErr)
	}
	p := f.project(t)
	projectMaterial := f.material(t, c.ID, " Project")
	f.exec(t, "INSERT INTO sitetrack.project_inventory(project_store_id,material_id,quantity,in_transit) VALUES($1,$2,0,1)", p.StoreID, projectMaterial.ID)
	if err := f.catalogue.Remove(ctx, f.users[1], projectMaterial.ID); !errors.Is(err, materials.ErrConflict) {
		t.Fatal("in-transit material removed", err)
	}
	if err := f.projects.Remove(ctx, f.users[0], p.ID); !errors.Is(err, projects.ErrStock) {
		t.Fatal("project with transit goods removed", err)
	}
	f.exec(t, "UPDATE sitetrack.project_inventory SET in_transit=0 WHERE project_store_id=$1", p.StoreID)
	if err := f.projects.Remove(ctx, f.users[0], p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryListsCursorSortsAndProjectAccess(t *testing.T) {
	f := inventoryTest(t)
	ctx := context.Background()
	a, b := f.category(t, " A"), f.category(t, " B")
	p := f.project(t)
	for i := range 23 {
		category := a.ID
		if i%2 != 0 {
			category = b.ID
		}
		m := f.material(t, category, fmt.Sprintf(" Item %02d", i))
		value := fmt.Sprintf("%d.125", i%13)
		if i%4 != 0 {
			if _, err := f.s.AddStock(ctx, f.users[1], m.ID, QuantityInput{Quantity: value}); err != nil {
				t.Fatal(err)
			}
		}
		if i%5 == 0 {
			if _, err := f.s.SetReorderPoint(ctx, f.users[1], m.ID, ReorderInput{ReorderPoint: "100"}); err != nil {
				t.Fatal(err)
			}
		}
		var delivered *time.Time
		if i%4 != 0 {
			date := time.Date(2026, 11, 11, 12+i%3, 0, 0, 0, time.UTC)
			delivered = &date
		}
		f.exec(t, "INSERT INTO sitetrack.project_inventory(project_store_id,material_id,quantity,last_delivery_at) VALUES($1,$2,$3::text::numeric,$4)", p.StoreID, m.ID, value, delivered)
	}
	for _, order := range []string{"asc", "desc"} {
		for _, sort := range []string{"name", "quantity", "status"} {
			input := CompanyListInput{Search: f.prefix, Sort: sort, Order: order, Limit: 3}
			seen := map[uuid.UUID]bool{}
			var previous *CompanyItem
			for {
				page, err := f.s.CompanyStock(ctx, f.users[1], input)
				if err != nil {
					t.Fatal(sort, order, err)
				}
				for _, item := range page.Items {
					if seen[item.MaterialID] {
						t.Fatal("duplicate company page item", sort, order)
					}
					seen[item.MaterialID] = true
					if previous != nil {
						comparison := strings.Compare(strings.ToLower(previous.Name), strings.ToLower(item.Name))
						if sort == "quantity" {
							left, _ := strconv.ParseInt(strings.ReplaceAll(previous.Quantity, ".", ""), 10, 64)
							right, _ := strconv.ParseInt(strings.ReplaceAll(item.Quantity, ".", ""), 10, 64)
							comparison = 0
							if left < right {
								comparison = -1
							}
							if left > right {
								comparison = 1
							}
						}
						if sort == "status" {
							states := []string{"out_of_stock", "low_stock", "available"}
							comparison = slices.Index(states, previous.Status) - slices.Index(states, item.Status)
						}
						if comparison == 0 {
							comparison = strings.Compare(previous.MaterialID.String(), item.MaterialID.String())
						}
						if (order == "asc" && comparison >= 0) || (order == "desc" && comparison <= 0) {
							t.Fatal("company sort is incorrect", sort, order)
						}
					}
					copy := item
					previous = &copy
				}
				if page.NextCursor == "" {
					break
				}
				input.Cursor = page.NextCursor
			}
			if len(seen) != 23 {
				t.Fatal("company pagination omitted materials", sort, order, len(seen))
			}
		}
		for _, sort := range []string{"name", "quantity", "last_delivery"} {
			input := ProjectListInput{Search: f.prefix, Sort: sort, Order: order, Limit: 3}
			seen := map[uuid.UUID]bool{}
			undated := false
			for {
				page, err := f.s.ProjectStock(ctx, f.users[0], p.ID, input)
				if err != nil {
					t.Fatal(sort, order, err)
				}
				for _, item := range page.Items {
					if seen[item.MaterialID] {
						t.Fatal("duplicate project page item", sort, order)
					}
					seen[item.MaterialID] = true
					if sort == "last_delivery" {
						if item.LastDeliveryAt == nil {
							undated = true
						} else if undated {
							t.Fatal("undated delivery not last")
						}
					}
				}
				if page.NextCursor == "" {
					break
				}
				input.Cursor = page.NextCursor
			}
			if len(seen) != 23 {
				t.Fatal("project pagination omitted materials", sort, order, len(seen))
			}
		}
	}
	filtered, err := f.s.CompanyStock(ctx, f.users[1], CompanyListInput{Search: f.prefix, CategoryID: a.ID.String(), Status: "out_of_stock", Sort: "name", Order: "asc", Limit: 100})
	if err != nil || len(filtered.Items) == 0 {
		t.Fatal("company filters failed", err)
	}
	for _, item := range filtered.Items {
		if item.CategoryID != a.ID || item.Quantity != "0.000" {
			t.Fatal("incorrect filtered item")
		}
	}
	literal, err := f.s.CompanyStock(ctx, f.users[1], CompanyListInput{Search: "%_", Sort: "name", Order: "asc", Limit: 20})
	if err != nil || len(literal.Items) != 0 {
		t.Fatal("search interpreted wildcard syntax", err)
	}
	input := ProjectListInput{Search: f.prefix, Sort: "name", Order: "asc", Limit: 1}
	page, err := f.s.ProjectStock(ctx, f.users[2], p.ID, input)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("SuperAdmin cannot read project stock", err)
	}
	input.Cursor = page.NextCursor
	input.Order = "desc"
	if _, err := f.s.ProjectStock(ctx, f.users[0], p.ID, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor accepted changed sorting", err)
	}
	input.Order = "asc"
	second := f.project(t)
	if _, err := f.s.ProjectStock(ctx, f.users[0], second.ID, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor accepted another project", err)
	}
	if _, err := f.s.ProjectStock(ctx, f.users[1], p.ID, ProjectListInput{Sort: "name", Order: "asc", Limit: 20}); !errors.Is(err, ErrNotFound) {
		t.Fatal("nonmember read project stock", err)
	}
	f.exec(t, "INSERT INTO sitetrack.project_memberships(project_id,user_id,is_admin) VALUES($1,$2,false)", p.ID, f.users[1])
	memberStock, err := f.s.ProjectStock(ctx, f.users[1], p.ID, ProjectListInput{Sort: "name", Order: "asc", Limit: 20})
	if err != nil || len(memberStock.Items) != 20 {
		t.Fatal("member cannot read project stock", err)
	}
	promote := true
	if err := f.projects.ChangeMember(ctx, f.users[0], p.ID, f.users[1], &promote); err != nil {
		t.Fatal(err)
	}
	memberStock, err = f.s.ProjectStock(ctx, f.users[1], p.ID, ProjectListInput{Sort: "name", Order: "asc", Limit: 20})
	if err != nil || len(memberStock.Items) != 20 {
		t.Fatal("promoted member cannot read project stock", err)
	}
	if err := f.projects.ChangeMember(ctx, f.users[0], p.ID, f.users[1], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectStock(ctx, f.users[1], p.ID, ProjectListInput{Sort: "name", Order: "asc", Limit: 20}); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed member retained stock access", err)
	}
}

func TestInventoryHTTPPermissionsContractsAndTelemetry(t *testing.T) {
	f := inventoryTest(t)
	ctx := context.Background()
	c := f.category(t, " HTTP")
	p := f.project(t)
	var item materials.Material
	for i := range 23 {
		item = f.material(t, c.ID, fmt.Sprintf(" HTTP %02d", i))
	}
	f.exec(t, "INSERT INTO sitetrack.project_inventory(project_store_id,material_id,quantity) VALUES($1,$2,2.125)", p.StoreID, item.ID)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := openapi.New()
	observe, err := f.monitoring.HTTP()
	if err != nil {
		t.Fatal(err)
	}
	router.Use(observe)
	if err := Register(router.Group(""), f.s, f.accounts, registry, false, f.monitoring.Logger); err != nil {
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
	call := func(method, path, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
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
	call("GET", "/company-inventory", tokens[1], "", 404)
	call("GET", "/projects/"+p.ID+"/inventory", tokens[0], "", 404)
	base := "/company-store/" + item.ID.String()
	for _, path := range []string{"/company-store", base} {
		call("GET", path, "", "", 401)
		call("GET", path, tokens[0], "", 403)
		call("GET", path, tokens[1], "", 200)
	}
	call("POST", base+"/add-stock", tokens[0], `{"quantity":"1"}`, 403)
	call("PUT", base+"/reorder-point", tokens[0], `{"reorder_point":"1"}`, 403)
	call("POST", base+"/add-stock", tokens[1], `{"quantity":0.125}`, 400)
	call("POST", base+"/add-stock", tokens[1], `{"quantity":"0.125"}`, 200)
	call("PUT", base+"/reorder-point", tokens[2], `{"reorder_point":"1"}`, 200)
	response := call("GET", "/company-store", tokens[1], "", 200)
	var page CompanyPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 20 || page.NextCursor == "" {
		t.Fatal("HTTP default page incorrect", err)
	}
	for _, query := range []string{"?limit=101", "?limit=0", "?status=invalid", "?cursor=bad", "?sort=invalid", "?category_id=bad"} {
		call("GET", "/company-store"+query, tokens[1], "", 400)
	}
	call("GET", "/company-store/not-an-id", tokens[1], "", 400)
	projectBase := "/projects/" + p.ID + "/store"
	for _, path := range []string{projectBase, projectBase + "/" + item.ID.String()} {
		denied := call("GET", path, tokens[1], "", 404)
		missing := call("GET", strings.Replace(path, p.ID, "PRJ-0000", 1), tokens[1], "", 404)
		if denied.Body.String() != missing.Body.String() {
			t.Fatal("stock resource leaked project existence")
		}
	}
	projectResponse := call("GET", projectBase, tokens[0], "", 200)
	var projectPage ProjectPage
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &projectPage); err != nil || len(projectPage.Items) != 1 || projectPage.Items[0].Quantity != "2.125" || strings.Contains(projectResponse.Body.String(), "can_manage") {
		t.Fatal("project store did not return stock", err)
	}
	call("GET", projectBase, tokens[2], "", 200)
	call("POST", projectBase+"/"+item.ID.String()+"/add-stock", tokens[0], `{"quantity":"1"}`, 404)
	f.exec(t, "UPDATE sitetrack.users SET role='user' WHERE id=$1", f.users[1])
	call("POST", base+"/add-stock", tokens[1], `{"quantity":"1"}`, 403)
	raw, err := registry.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var document openapi.Document
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Paths) != 6 {
		t.Fatal("missing inventory routes", len(document.Paths))
	}
	if strings.Contains(string(raw), "can_manage") || strings.Contains(string(raw), "/company-inventory") || strings.Contains(string(raw), "/inventory") {
		t.Fatal("obsolete store contract remains")
	}
	if strings.Contains(response.Body.String(), "store_id") || strings.Contains(response.Body.String(), "can_manage") {
		t.Fatal("company stock returned store metadata")
	}
	for _, methods := range document.Paths {
		for _, op := range methods {
			for _, response := range op.Responses {
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
							t.Fatal("invalid inventory example", op.OperationID, err)
						}
					}
				}
			}
		}
	}
	spans := f.spans.GetSpans()
	found := false
	for _, sql := range spans {
		if sql.Name == "AddStock" {
			for _, business := range spans {
				if business.Name == "inventory.company.add_stock" && sql.Parent.SpanID() == business.SpanContext.SpanID() {
					for _, http := range spans {
						if business.Parent.SpanID() == http.SpanContext.SpanID() && strings.HasPrefix(http.Name, "POST ") {
							found = true
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("HTTP/business/database trace chain missing")
	}
	var measured metricdata.ResourceMetrics
	if err := f.metrics.Collect(ctx, &measured); err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]bool{}
	for _, scope := range measured.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == "inventory.operations" {
				if sum, ok := metric.Data.(metricdata.Sum[int64]); ok {
					for _, point := range sum.DataPoints {
						if point.Attributes.Len() != 2 {
							t.Fatal("unbounded inventory metric labels")
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
	if !outcomes["success"] || !outcomes["rejected"] || !strings.Contains(f.logs.String(), "trace_id") || strings.Contains(f.logs.String(), item.Name) {
		t.Fatal("inventory telemetry missing or includes input values")
	}
}

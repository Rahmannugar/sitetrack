package deliveries

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rahmannugar/authlier/password"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"sitetrack/internal/authentication"
	"sitetrack/internal/config"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/telemetry"
	"sitetrack/internal/inventory"
	"sitetrack/internal/materials"
	"sitetrack/internal/projects"
)

type fixture struct {
	s            *Service
	pool         *pgxpool.Pool
	stock        *inventory.Service
	projects     *projects.Service
	catalogue    *materials.Service
	accounts     *authentication.Service
	monitoring   *telemetry.Runtime
	users        []uuid.UUID
	projectsList []projects.Project
	category     uuid.UUID
	materialIDs  []uuid.UUID
	spans        *tracetest.InMemoryExporter
	metrics      *sdkmetric.ManualReader
	logs         *bytes.Buffer
}

func deliveryTest(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	logs := &bytes.Buffer{}
	monitoring, err := telemetry.New(ctx, config.Telemetry{}, "deliveries-test", logs)
	if err != nil {
		t.Fatal(err)
	}
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = tp.Shutdown(ctx); _ = mp.Shutdown(ctx) })
	monitoring.Tracer = tp.Tracer("deliveries-test")
	monitoring.Meter = mp.Meter("deliveries-test")
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
	stock, err := inventory.NewService(pool, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := materials.NewService(pool, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := authentication.NewService(pool, config.Authentication{OTPSecret: []byte("12345678901234567890123456789012"), OutboxKey: []byte("12345678901234567890123456789012"), JWTSeed: []byte("12345678901234567890123456789012")})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: service, pool: pool, stock: stock, catalogue: catalogue, projects: projects.NewService(pool), accounts: accounts, monitoring: monitoring, spans: spans, metrics: reader, logs: logs}
	t.Cleanup(func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer rollback(tx)
		for _, sql := range []string{
			"DELETE FROM sitetrack.delivery_items WHERE delivery_id IN (SELECT id FROM sitetrack.deliveries WHERE initiated_by_user_id=ANY($1))",
			"DELETE FROM sitetrack.deliveries WHERE initiated_by_user_id=ANY($1)",
		} {
			if _, err := tx.Exec(ctx, sql, f.users); err != nil {
				t.Error(err)
				return
			}
		}
		for _, sql := range []string{"DELETE FROM sitetrack.company_inventory WHERE material_id=ANY($1)", "DELETE FROM sitetrack.project_inventory WHERE material_id=ANY($1)", "DELETE FROM sitetrack.materials WHERE id=ANY($1)"} {
			if _, err := tx.Exec(ctx, sql, f.materialIDs); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM sitetrack.material_categories WHERE id=$1", f.category); err != nil {
			t.Error(err)
			return
		}
		for _, p := range f.projectsList {
			for _, sql := range []string{"UPDATE sitetrack.projects SET deleted_at=now() WHERE id=$1", "DELETE FROM sitetrack.notifications WHERE project_id=$1", "DELETE FROM sitetrack.project_memberships WHERE project_id=$1", "DELETE FROM sitetrack.project_stores WHERE project_id=$1", "DELETE FROM sitetrack.projects WHERE id=$1"} {
				if _, err := tx.Exec(ctx, sql, p.ID); err != nil {
					t.Error(err)
					return
				}
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
	hash, err := password.Hash("A-secure-password-123")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"user", "company_store_manager", "superadmin", "user", "user"} {
		id := uuid.Must(uuid.NewV7())
		f.exec(t, "INSERT INTO sitetrack.users(id,name,email,password_hash,role,email_verified) VALUES($1,'Delivery Engineer',$2,$3,$4,true)", id, id.String()+"@example.test", hash, role)
		f.users = append(f.users, id)
	}
	for range 2 {
		p, err := f.projects.Create(ctx, f.users[0], projects.Input{Name: "Delivery Site", StartDate: "2026-11-01", EndDate: "2026-12-01"})
		if err != nil {
			t.Fatal(err)
		}
		f.projectsList = append(f.projectsList, p)
	}
	f.exec(t, "INSERT INTO sitetrack.project_memberships(project_id,user_id,is_admin) VALUES($1,$2,false)", f.projectsList[0].ID, f.users[3])
	category, err := catalogue.CreateCategory(ctx, f.users[1], materials.CategoryInput{Name: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	f.category = category.ID
	for range 2 {
		material, err := catalogue.Create(ctx, f.users[1], materials.Input{Name: uuid.NewString(), CategoryID: f.category.String(), Unit: "bag"})
		if err != nil {
			t.Fatal(err)
		}
		f.materialIDs = append(f.materialIDs, material.ID)
	}
	return f
}
func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) add(t *testing.T, index int, value string) {
	t.Helper()
	if _, err := f.stock.AddStock(context.Background(), f.users[1], f.materialIDs[index], inventory.QuantityInput{Quantity: value}); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) send(t *testing.T, direction, amount string) Delivery {
	t.Helper()
	actor := f.users[1]
	if direction == "project_to_company" {
		actor = f.users[0]
	}
	d, err := f.s.Create(context.Background(), actor, Input{ProjectID: f.projectsList[0].ID, Direction: direction, Items: []LineInput{{MaterialID: f.materialIDs[0].String(), Quantity: amount}}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func (f *fixture) move(t *testing.T, d Delivery, action string) Delivery {
	t.Helper()
	actor := f.users[0]
	if (d.Direction == "company_to_project") == (action == "cancel") {
		actor = f.users[1]
	}
	result, err := f.s.Transition(context.Background(), actor, d.ID, action)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func (f *fixture) balances(t *testing.T, company, project, companyTransit, projectTransit string) {
	t.Helper()
	var c, p, ct, pt string
	if err := f.pool.QueryRow(context.Background(), `SELECT c.quantity::text,p.quantity::text,c.in_transit_to_projects::text,p.in_transit::text FROM sitetrack.company_inventory c JOIN sitetrack.project_inventory p USING(material_id) WHERE c.material_id=$1 AND p.project_store_id=$2`, f.materialIDs[0], f.projectsList[0].StoreID).Scan(&c, &p, &ct, &pt); err != nil {
		t.Fatal(err)
	}
	if c != company || p != project || ct != companyTransit || pt != projectTransit {
		t.Fatalf("balances %s %s %s %s; want %s %s %s %s", c, p, ct, pt, company, project, companyTransit, projectTransit)
	}
}
func TestTwoWayDeliveryLifecycleAndAtomicFailures(t *testing.T) {
	f := deliveryTest(t)
	ctx := context.Background()
	f.add(t, 0, "10")
	sent := f.send(t, "company_to_project", "4.125")
	if sent.ID.Version() != 7 || sent.Status != "in_transit" || len(sent.Items) != 1 {
		t.Fatal("invalid delivery", sent)
	}
	f.balances(t, "5.875", "0.000", "4.125", "0.000")
	if err := f.projects.Remove(ctx, f.users[0], sent.ProjectID); !errors.Is(err, projects.ErrStock) {
		t.Fatal("incoming delivery did not protect project", err)
	}
	if err := f.catalogue.Remove(ctx, f.users[1], f.materialIDs[0]); !errors.Is(err, materials.ErrConflict) {
		t.Fatal("transit material removed", err)
	}
	var notified int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.notifications WHERE project_id=$1 AND type='delivery.sent'", sent.ProjectID).Scan(&notified); err != nil || notified != 2 {
		t.Fatal("delivery notifications missing", notified, err)
	}
	received := f.move(t, sent, "receive")
	if received.Status != "delivered" || received.CompletedAt == nil || received.CompletedByUserID == nil || *received.CompletedByUserID != f.users[0] {
		t.Fatal("missing receiving history")
	}
	f.balances(t, "5.875", "4.125", "0.000", "0.000")
	if _, err := f.s.Transition(ctx, f.users[0], sent.ID, "receive"); !errors.Is(err, ErrState) {
		t.Fatal("delivery received twice", err)
	}
	back := f.send(t, "project_to_company", "2.001")
	f.balances(t, "5.875", "2.124", "0.000", "2.001")
	f.move(t, back, "receive")
	f.balances(t, "7.876", "2.124", "0.000", "0.000")
	for _, direction := range []string{"company_to_project", "project_to_company"} {
		for _, action := range []string{"cancel", "reject"} {
			d := f.send(t, direction, "0.125")
			f.move(t, d, action)
			f.balances(t, "7.876", "2.124", "0.000", "0.000")
		}
	}
	if _, err := f.pool.Exec(ctx, "UPDATE sitetrack.deliveries SET direction='project_to_company' WHERE id=$1", sent.ID); err == nil {
		t.Fatal("delivery history was editable")
	}
	if _, err := f.pool.Exec(ctx, "UPDATE sitetrack.delivery_items SET quantity=1 WHERE delivery_id=$1", sent.ID); err == nil {
		t.Fatal("delivery material quantities were editable")
	}
	if _, err := f.s.Create(ctx, f.users[1], Input{ProjectID: sent.ProjectID, Direction: "company_to_project", Items: []LineInput{{f.materialIDs[0].String(), "1"}, {f.materialIDs[1].String(), "1"}}}); !errors.Is(err, ErrStock) {
		t.Fatal("partial stock failure accepted", err)
	}
	f.balances(t, "7.876", "2.124", "0.000", "0.000")
	f.exec(t, `CREATE FUNCTION sitetrack.reject_delivery_notification() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type LIKE 'delivery.%' THEN RAISE EXCEPTION 'forced notification failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_delivery_notification BEFORE INSERT ON sitetrack.notifications FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_delivery_notification()`)
	defer f.exec(t, "DROP TRIGGER IF EXISTS reject_delivery_notification ON sitetrack.notifications;DROP FUNCTION IF EXISTS sitetrack.reject_delivery_notification()")
	if _, err := f.s.Create(ctx, f.users[1], Input{ProjectID: sent.ProjectID, Direction: "company_to_project", Items: []LineInput{{f.materialIDs[0].String(), "1"}}}); err == nil {
		t.Fatal("notification failure did not roll back delivery")
	}
	f.balances(t, "7.876", "2.124", "0.000", "0.000")
}
func TestConcurrentDeliveriesAndTerminalActions(t *testing.T) {
	f := deliveryTest(t)
	ctx := context.Background()
	f.add(t, 0, "10")
	f.add(t, 1, "10")
	var group sync.WaitGroup
	start := make(chan struct{})
	results := make(chan Delivery, 2)
	for index := range 2 {
		group.Go(func() {
			<-start
			items := []LineInput{{f.materialIDs[index].String(), "5"}, {f.materialIDs[1-index].String(), "5"}}
			d, err := f.s.Create(ctx, f.users[1], Input{ProjectID: f.projectsList[index].ID, Direction: "company_to_project", Items: items})
			if err != nil {
				t.Error(err)
			}
			results <- d
		})
	}
	close(start)
	group.Wait()
	close(results)
	var first Delivery
	for d := range results {
		if d.ID == uuid.Nil {
			t.Fatal("concurrent initiation failed")
		}
		if d.ProjectID == f.projectsList[0].ID {
			first = d
		}
	}
	f.balances(t, "0.000", "0.000", "10.000", "0.000")
	if _, err := f.s.Create(ctx, f.users[1], Input{ProjectID: first.ProjectID, Direction: "company_to_project", Items: []LineInput{{f.materialIDs[0].String(), "0.001"}}}); !errors.Is(err, ErrStock) {
		t.Fatal("stock went negative", err)
	}
	errorsFound := make(chan error, 2)
	start = make(chan struct{})
	group.Go(func() { <-start; _, err := f.s.Transition(ctx, f.users[1], first.ID, "cancel"); errorsFound <- err })
	group.Go(func() { <-start; _, err := f.s.Transition(ctx, f.users[0], first.ID, "receive"); errorsFound <- err })
	close(start)
	group.Wait()
	a, b := <-errorsFound, <-errorsFound
	if !((a == nil && errors.Is(b, ErrState)) || (b == nil && errors.Is(a, ErrState))) {
		t.Fatal("terminal race", a, b)
	}
	d, err := f.s.Get(ctx, f.users[1], first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status == "delivered" {
		f.balances(t, "0.000", "5.000", "5.000", "0.000")
	} else if d.Status == "cancelled" {
		f.balances(t, "5.000", "0.000", "5.000", "0.000")
	} else {
		t.Fatal(d.Status)
	}
}
func TestDeliveryCapacityKeepsReturnsPossible(t *testing.T) {
	f := deliveryTest(t)
	ctx := context.Background()
	f.add(t, 0, "999999999999999.999")
	pending := f.send(t, "company_to_project", "1")
	if _, err := f.stock.AddStock(ctx, f.users[1], f.materialIDs[0], inventory.QuantityInput{Quantity: "0.001"}); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("supplier stock consumed reserved return capacity", err)
	}
	f.move(t, pending, "cancel")
	f.balances(t, "999999999999999.999", "0.000", "0.000", "0.000")
	all := f.send(t, "company_to_project", "999999999999999.999")
	f.move(t, all, "receive")
	outbound := f.send(t, "project_to_company", "1")
	f.add(t, 0, "1")
	inbound := f.send(t, "company_to_project", "1")
	if _, err := f.s.Transition(ctx, f.users[0], inbound.ID, "receive"); !errors.Is(err, ErrCapacity) {
		t.Fatal("receipt consumed reserved project return capacity", err)
	}
	f.balances(t, "0.000", "999999999999998.999", "1.000", "1.000")
	current, err := f.s.Get(ctx, f.users[0], inbound.ID)
	if err != nil || current.Status != "in_transit" {
		t.Fatal("failed receipt changed delivery", err)
	}
	f.move(t, inbound, "cancel")
	f.move(t, outbound, "cancel")
	f.balances(t, "1.000", "999999999999999.999", "0.000", "0.000")
	outbound = f.send(t, "project_to_company", "1")
	f.add(t, 0, "999999999999998.999")
	if _, err := f.s.Transition(ctx, f.users[1], outbound.ID, "receive"); !errors.Is(err, ErrCapacity) {
		t.Fatal("overflowing company receipt accepted", err)
	}
	f.move(t, outbound, "reject")
	f.balances(t, "999999999999999.999", "999999999999999.999", "0.000", "0.000")
}

func TestDeliveryHTTPAccessPaginationContractsAndTelemetry(t *testing.T) {
	f := deliveryTest(t)
	ctx := context.Background()
	f.add(t, 0, "1")
	var first Delivery
	for range 23 {
		first = f.send(t, "company_to_project", "0.001")
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := openapi.New()
	observe, err := f.monitoring.HTTP()
	if err != nil {
		t.Fatal(err)
	}
	router.Use(observe)
	if err := inventory.Register(router.Group(""), f.stock, f.accounts, registry, false, f.monitoring.Logger); err != nil {
		t.Fatal(err)
	}
	if err := Register(router.Group(""), f.s, f.accounts, registry, false, f.monitoring.Logger); err != nil {
		t.Fatal(err)
	}
	tokens := make([]string, len(f.users))
	for i, id := range f.users {
		pair, err := f.accounts.Login(ctx, authentication.LoginInput{Email: id.String() + "@example.test", Password: "A-secure-password-123"})
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
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	project := "/projects/" + first.ProjectID + "/deliveries"
	detail := "/deliveries/" + first.ID.String()
	call("GET", project, "", "", 401)
	call("GET", project, tokens[4], "", 404)
	call("GET", project, tokens[1], "", 404)
	call("GET", "/company-store/deliveries", tokens[0], "", 403)
	call("GET", detail, tokens[1], "", 200)
	call("GET", detail, tokens[3], "", 200)
	denied := call("GET", detail, tokens[4], "", 404)
	missing := call("GET", "/deliveries/"+uuid.NewString(), tokens[4], "", 404)
	if denied.Body.String() != missing.Body.String() {
		t.Fatal("private delivery existence leaked")
	}
	call("POST", detail+"/receive", tokens[3], "", 403)
	call("POST", detail+"/receive", tokens[2], "", 403)
	call("POST", detail+"/cancel", tokens[0], "", 403)
	f.spans.Reset()
	pageResponse := call("GET", project, tokens[3], "", 200)
	var page Page
	if err := json.Unmarshal(pageResponse.Body.Bytes(), &page); err != nil || len(page.Items) != 20 || page.NextCursor == "" {
		t.Fatal("default page", err)
	}
	reads := 0
	for _, span := range f.spans.GetSpans() {
		if span.Name == "DeliveryItems" {
			reads++
		}
	}
	if reads != 1 {
		t.Fatal("delivery page did not batch item reads", reads)
	}
	next := call("GET", project+"?cursor="+page.NextCursor, tokens[3], "", 200)
	var tail Page
	if err := json.Unmarshal(next.Body.Bytes(), &tail); err != nil || len(tail.Items) != 3 || tail.NextCursor != "" {
		t.Fatal("cursor page", err)
	}
	if page.Items[19].ID == tail.Items[0].ID {
		t.Fatal("cursor duplicated delivery")
	}
	call("GET", project+"?status=delivered&cursor="+page.NextCursor, tokens[0], "", 400)
	call("GET", "/projects/"+f.projectsList[1].ID+"/deliveries?cursor="+page.NextCursor, tokens[0], "", 400)
	call("GET", project+"?limit=101", tokens[0], "", 400)
	date := first.CreatedAt.UTC().Format(time.DateOnly)
	filtered := call("GET", "/company-store/deliveries?project_id="+first.ProjectID+"&direction=company_to_project&status=in_transit&from="+date+"&to="+date, tokens[1], "", 200)
	if !strings.Contains(filtered.Body.String(), first.ID.String()) {
		t.Fatal("company filters omitted delivery")
	}
	input := Input{ProjectID: first.ProjectID, Direction: "company_to_project", Items: []LineInput{{f.materialIDs[0].String(), "0.001"}}}
	raw, _ := json.Marshal(input)
	for _, body := range []string{`{}`, `{"project_id":"PRJ-0001","direction":"company_to_project","items":[]}`, fmt.Sprintf(`{"project_id":%q,"direction":"company_to_project","items":[{"material_id":%q,"quantity":"-1"}]}`, first.ProjectID, f.materialIDs[0])} {
		call("POST", "/deliveries", tokens[1], body, 400)
	}
	duplicate := input
	duplicate.Items = append(duplicate.Items, duplicate.Items[0])
	duplicateRaw, _ := json.Marshal(duplicate)
	call("POST", "/deliveries", tokens[1], string(duplicateRaw), 400)
	call("POST", "/deliveries", tokens[3], string(raw), 403)
	call("POST", "/deliveries", tokens[1], string(raw), 201)
	call("POST", detail+"/receive", tokens[0], "", 200)
	f.exec(t, "UPDATE sitetrack.users SET role='user' WHERE id=$1", f.users[1])
	call("POST", detail+"/cancel", tokens[1], "", 404)
	call("GET", "/company-store/deliveries", tokens[1], "", 403)
	if err := f.projects.ChangeMember(ctx, f.users[0], first.ProjectID, f.users[3], nil); err != nil {
		t.Fatal(err)
	}
	call("GET", project, tokens[3], "", 404)
	call("GET", detail, tokens[3], "", 404)
	specification, err := registry.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var document openapi.Document
	if err := json.Unmarshal(specification, &document); err != nil {
		t.Fatal(err)
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
							t.Fatal("invalid example", op.OperationID, err)
						}
					}
				}
			}
		}
	}
	createSchema := document.Paths["/deliveries"]["post"].RequestBody.Content["application/json"].Schema
	resolved, err := createSchema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var valid map[string]any
	if err := json.Unmarshal(raw, &valid); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(valid); err != nil {
		t.Fatal("generated delivery request invalid", err)
	}
	valid["items"] = []any{map[string]any{"quantity": "1"}}
	if err := resolved.Validate(valid); err == nil {
		t.Fatal("nested request contract lost required material")
	}
	found := false
	spans := f.spans.GetSpans()
	for _, sql := range spans {
		if sql.Name == "SendCompanyStock" {
			for _, business := range spans {
				if business.Name == "deliveries.create" && sql.Parent.SpanID() == business.SpanContext.SpanID() {
					for _, http := range spans {
						if strings.HasPrefix(http.Name, "POST ") && business.Parent.SpanID() == http.SpanContext.SpanID() {
							found = true
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("delivery HTTP/business/SQL trace chain missing")
	}
	var measured metricdata.ResourceMetrics
	if err := f.metrics.Collect(ctx, &measured); err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]bool{}
	for _, scope := range measured.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == "deliveries.operations" {
				if sum, ok := metric.Data.(metricdata.Sum[int64]); ok {
					for _, point := range sum.DataPoints {
						if point.Attributes.Len() != 2 {
							t.Fatal("unbounded delivery metric labels")
						}
						for _, attr := range point.Attributes.ToSlice() {
							if string(attr.Key) == "outcome" {
								outcomes[attr.Value.AsString()] = true
							}
						}
					}
				}
			}
		}
	}
	if !outcomes["success"] || !outcomes["rejected"] || !strings.Contains(f.logs.String(), "trace_id") || strings.Contains(f.logs.String(), "A-secure-password") {
		t.Fatal("delivery telemetry missing or contains credentials")
	}
}

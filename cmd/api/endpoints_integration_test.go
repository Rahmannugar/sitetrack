package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"sitetrack/internal/authentication"
	"sitetrack/internal/config"
	"sitetrack/internal/deliveries"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/infra/outbox"
	"sitetrack/internal/infra/telemetry"
	"sitetrack/internal/inventory"
	"sitetrack/internal/materials"
	"sitetrack/internal/notifications"
	"sitetrack/internal/projects"
	"sitetrack/internal/tasks"
)

type endpointResult struct {
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	Status       int      `json:"status"`
	Milliseconds float64  `json:"milliseconds"`
	Plans        []string `json:"plans"`
}
type endpointCheck struct {
	t       *testing.T
	server  *httptest.Server
	client  *http.Client
	routes  gin.RoutesInfo
	results map[string]endpointResult
	pool    *pgxpool.Pool
	cipher  *outbox.PayloadCipher
}

func (h *endpointCheck) pattern(method, path string) string {
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(path, "/")
	best := ""
	score := -1
	for _, route := range h.routes {
		if route.Method != method {
			continue
		}
		tokens := strings.Split(route.Path, "/")
		if len(parts) != len(tokens) {
			continue
		}
		match, n := true, 0
		for i, token := range tokens {
			if strings.HasPrefix(token, ":") {
				continue
			}
			if parts[i] != token {
				match = false
				break
			}
			n++
		}
		if match && n > score {
			best = route.Path
			score = n
		}
	}
	if best == "" {
		h.t.Fatalf("unregistered endpoint %s %s", method, path)
	}
	return method + " " + best
}
func (h *endpointCheck) request(method, path, token string, body any, status int) map[string]json.RawMessage {
	h.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, h.server.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	start := time.Now()
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: request failed", method, path)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		h.t.Fatal(err)
	}
	if res.StatusCode != status {
		h.t.Fatalf("%s %s: got %d, wanted %d", method, path, res.StatusCode, status)
	}
	key := h.pattern(method, path)
	h.results[key] = endpointResult{Method: method, Path: strings.TrimPrefix(key, method+" "), Status: status, Milliseconds: float64(time.Since(start).Microseconds()) / 1000}
	var result map[string]json.RawMessage
	if strings.Contains(res.Header.Get("Content-Type"), "json") {
		if err := json.Unmarshal(data, &result); err != nil {
			h.t.Fatalf("invalid response for %s", key)
		}
	}
	return result
}
func value(body map[string]json.RawMessage, key string) string {
	var text string
	_ = json.Unmarshal(body[key], &text)
	return text
}
func (h *endpointCheck) login(email string) string {
	return value(h.request("POST", "/auth/login", "", map[string]any{"email": email, "password": "SiteTrack123!"}, 200), "access_token")
}
func (h *endpointCheck) code(email, kind string) string {
	h.t.Helper()
	var id uuid.UUID
	var payload []byte
	err := h.pool.QueryRow(context.Background(), "SELECT e.id,e.payload FROM sitetrack.outbox_events e JOIN sitetrack.users u ON u.id::text=e.aggregate_id WHERE u.email=$1 AND e.event_type=$2 AND e.payload IS NOT NULL ORDER BY e.created_at DESC,e.id DESC LIMIT 1", email, kind).Scan(&id, &payload)
	if err != nil {
		h.t.Fatal("OTP job missing")
	}
	raw, err := h.cipher.Open(payload, id[:])
	if err != nil {
		h.t.Fatal("OTP job decryption failed")
	}
	var message struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &message) != nil || message.Code == "" {
		h.t.Fatal("OTP payload invalid")
	}
	return message.Code
}
func (h *endpointCheck) stream(token string, cursor string) string {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.server.URL+"/notifications/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if cursor != "" {
		req.Header.Set("Last-Event-ID", cursor)
	}
	start := time.Now()
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal("SSE connection failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		h.t.Fatal("SSE response invalid")
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	seen := false
	nextCursor := ""
	event := ""
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "event:"))
		}
		if strings.HasPrefix(scanner.Text(), "id:") {
			nextCursor = strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "id:"))
		}
		if strings.HasPrefix(scanner.Text(), "data:") && ((cursor == "" && event == "snapshot") || (cursor != "" && event == "notifications")) {
			seen = true
			break
		}
	}
	if !seen {
		h.t.Fatal("SSE snapshot missing")
	}
	h.results["GET /notifications/stream"] = endpointResult{Method: "GET", Path: "/notifications/stream", Status: 200, Milliseconds: float64(time.Since(start).Microseconds()) / 1000}
	return nextCursor
}
func TestEveryEndpointWithQueryPlans(t *testing.T) {
	url := os.Getenv("SITETRACK_ENDPOINT_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_ENDPOINT_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	parsed, err := pgxpool.ParseConfig(url)
	if err != nil || !strings.HasSuffix(parsed.ConnConfig.Database, "_endpoint_check") {
		t.Fatal("use a disposable full-scale clone ending in _endpoint_check")
	}
	pool, err := database.Open(ctx, url, 15)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.tasks").Scan(&count); err != nil || count < 500000 {
		t.Fatal("full-scale seed required")
	}
	plans := &endpointPlans{plans: map[string]json.RawMessage{}, routes: map[string]map[string]bool{}}
	traced, err := database.Open(ctx, url, 15, database.WithTracer(plans))
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	monitoring, err := telemetry.New(ctx, config.Telemetry{}, "endpoint-check", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer monitoring.Shutdown(context.Background())
	settings := config.Authentication{OTPSecret: []byte("12345678901234567890123456789012"), OutboxKey: []byte("abcdefghijklmnopqrstuvxyz1234567"), JWTSecret: []byte("12345678901234567890123456789012"), EmailFrom: "SiteTrack <sender@example.test>"}
	accounts, err := authentication.NewService(traced, settings)
	if err != nil {
		t.Fatal(err)
	}
	service := notifications.NewService(traced, ctx, monitoring.Logger)
	done := make(chan struct{})
	go func() { defer close(done); service.Run() }()
	defer func() { cancel(); <-done }()
	catalogue, err := materials.NewService(traced, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	stock, err := inventory.NewService(traced, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	movement, err := deliveries.NewService(traced, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	router, err := newRouter(traced, monitoring, accounts, projects.NewService(traced), service, tasks.NewService(traced), catalogue, stock, movement, false)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := outbox.NewPayloadCipher(settings.OutboxKey)
	h := &endpointCheck{t: t, client: &http.Client{Timeout: 15 * time.Second}, routes: router.Routes(), results: map[string]endpointResult{}, pool: pool, cipher: cipher}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := h.pattern(r.Method, r.URL.Path)
		router.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), endpointKey{}, key)))
	}))
	defer h.server.Close()
	h.request("GET", "/health/live", "", nil, 200)
	h.request("GET", "/health/ready", "", nil, 200)
	h.request("GET", "/docs", "", nil, 200)
	h.request("GET", "/openapi.json", "", nil, 200)
	admin := h.login("admin@example.test")
	manager := h.login("manager@example.test")
	root := h.login("superadmin@example.test")
	email := "endpoint-" + uuid.NewString() + "@example.test"
	h.request("POST", "/auth/signup", "", map[string]any{"name": "Endpoint User", "email": email, "password": "SiteTrack123!", "role": "user"}, 201)
	if _, err := pool.Exec(ctx, "UPDATE sitetrack.email_verifications SET created_at=created_at-interval '2 minutes' WHERE user_id=(SELECT id FROM sitetrack.users WHERE email=$1)", email); err != nil {
		t.Fatal(err)
	}
	h.request("POST", "/auth/resend-verification", "", map[string]any{"email": email}, 202)
	h.request("POST", "/auth/verify-email", "", map[string]any{"email": email, "code": h.code(email, "email.verification")}, 200)
	logged := h.request("POST", "/auth/login", "", map[string]any{"email": email, "password": "SiteTrack123!"}, 200)
	member := value(logged, "access_token")
	refresh := h.request("POST", "/auth/refresh", "", map[string]any{"refresh_token": value(logged, "refresh_token")}, 200)
	member = value(refresh, "access_token")
	profile := h.request("GET", "/auth/me", member, nil, 200)
	user := value(profile, "id")
	h.request("POST", "/auth/forgot-password", "", map[string]any{"email": email}, 202)
	h.request("POST", "/auth/reset-password", "", map[string]any{"email": email, "code": h.code(email, "email.password_reset"), "password": "SiteTrack123!"}, 200)
	member = h.login(email)
	dates := map[string]any{"name": "Endpoint Site", "start_date": "2026-01-01", "end_date": "2030-12-31"}
	project := value(h.request("POST", "/projects", admin, dates, 201), "id")
	p := "/projects/" + project
	h.request("GET", "/projects", root, nil, 200)
	h.request("GET", p, admin, nil, 200)
	h.request("PUT", p, admin, dates, 200)
	invite := value(h.request("POST", p+"/invitations", admin, map[string]any{"invitee_id": user}, 201), "id")
	h.request("GET", p+"/invitations", admin, nil, 200)
	h.request("POST", "/invitations/"+invite+"/respond", member, map[string]any{"action": "accept"}, 200)
	var outsider uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM sitetrack.users WHERE email='outsider@example.test'").Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	withdrawn := value(h.request("POST", p+"/invitations", admin, map[string]any{"invitee_id": outsider.String()}, 201), "id")
	h.request("DELETE", p+"/invitations/"+withdrawn, admin, nil, 200)
	h.request("GET", p+"/members", admin, nil, 200)
	h.request("PATCH", p+"/members/"+user, admin, map[string]any{"is_admin": true}, 204)
	h.request("PATCH", p+"/members/"+user, admin, map[string]any{"is_admin": false}, 204)
	taskA := value(h.request("POST", p+"/tasks", admin, dates, 201), "id")
	taskB := value(h.request("POST", p+"/tasks", admin, dates, 201), "id")
	a := p + "/tasks/" + taskA
	b := p + "/tasks/" + taskB
	h.request("PUT", a, admin, dates, 200)
	h.request("GET", a, member, nil, 200)
	h.request("GET", p+"/tasks", member, nil, 200)
	h.request("POST", b+"/dependencies", admin, map[string]any{"required_task_id": taskA}, 201)
	h.request("GET", b+"/dependencies", member, nil, 200)
	h.request("GET", b+"/relationships", member, nil, 200)
	h.request("POST", a+"/start", admin, nil, 200)
	h.request("POST", a+"/done", admin, nil, 200)
	h.request("POST", a+"/reopen", admin, nil, 200)
	h.request("DELETE", b+"/dependencies/"+taskA, admin, nil, 204)
	h.request("DELETE", b, admin, nil, 204)
	h.request("DELETE", a, admin, nil, 204)
	category := value(h.request("POST", "/material-categories", manager, map[string]any{"name": "Endpoint " + uuid.NewString()}, 201), "id")
	c := "/material-categories/" + category
	h.request("GET", "/material-categories", member, nil, 200)
	h.request("GET", c, member, nil, 200)
	h.request("PUT", c, manager, map[string]any{"name": "Updated " + uuid.NewString()}, 200)
	materialInput := map[string]any{"name": "Endpoint Material " + uuid.NewString(), "category_id": category, "unit": "bag"}
	material := value(h.request("POST", "/materials", manager, materialInput, 201), "id")
	m := "/materials/" + material
	h.request("GET", "/materials", member, nil, 200)
	h.request("GET", m, member, nil, 200)
	h.request("PUT", m, manager, materialInput, 200)
	var stockMaterial uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT material_id FROM sitetrack.company_store WHERE quantity>10 ORDER BY material_id LIMIT 1").Scan(&stockMaterial); err != nil {
		t.Fatal(err)
	}
	store := "/company-store/" + stockMaterial.String()
	h.request("POST", store+"/add-stock", manager, map[string]any{"quantity": "1.000"}, 200)
	h.request("PUT", store+"/reorder-point", manager, map[string]any{"reorder_point": "10.000"}, 200)
	h.request("GET", "/company-store", manager, nil, 200)
	h.request("GET", store, manager, nil, 200)
	send := func(direction, token string) string {
		return value(h.request("POST", "/deliveries", token, map[string]any{"project_id": project, "direction": direction, "items": []map[string]any{{"material_id": stockMaterial.String(), "quantity": "1.000"}}}, 201), "id")
	}
	first := send("company_to_project", manager)
	h.request("GET", "/deliveries/"+first, member, nil, 200)
	h.request("POST", "/deliveries/"+first+"/receive", admin, nil, 200)
	rejected := send("project_to_company", admin)
	h.request("POST", "/deliveries/"+rejected+"/reject", manager, nil, 200)
	returned := send("project_to_company", admin)
	h.request("POST", "/deliveries/"+returned+"/receive", manager, nil, 200)
	cancelled := send("company_to_project", manager)
	h.request("POST", "/deliveries/"+cancelled+"/cancel", manager, nil, 200)
	h.request("GET", "/company-store/deliveries", manager, nil, 200)
	h.request("GET", p+"/deliveries", member, nil, 200)
	h.request("GET", p+"/store", member, nil, 200)
	h.request("GET", p+"/store/"+stockMaterial.String(), member, nil, 200)
	page := h.request("GET", "/notifications", member, nil, 200)
	var notes []map[string]json.RawMessage
	_ = json.Unmarshal(page["items"], &notes)
	if len(notes) == 0 {
		t.Fatal("notification missing")
	}
	h.request("GET", "/notifications/unread-count", member, nil, 200)
	streamCursor := h.stream(member, "")
	h.request("PATCH", "/notifications/"+value(notes[0], "id")+"/read", member, nil, 204)
	h.stream(member, streamCursor)
	h.request("PATCH", "/notifications/read-all", member, nil, 204)
	h.request("DELETE", m, manager, nil, 204)
	h.request("DELETE", c, manager, nil, 204)
	h.request("DELETE", p+"/members/"+user, admin, nil, 204)
	h.request("DELETE", p, admin, nil, 204)
	h.request("POST", "/auth/logout", member, nil, 200)
	h.server.Close()
	for _, route := range h.routes {
		if _, ok := h.results[route.Method+" "+route.Path]; !ok {
			t.Errorf("endpoint not exercised: %s %s", route.Method, route.Path)
		}
	}
	plans.mu.Lock()
	for _, failure := range plans.errors {
		t.Error(failure)
	}
	for route, queries := range plans.routes {
		for key := range queries {
			if _, ok := plans.plans[key]; !ok {
				t.Errorf("SQL plan missing: %s %s", route, key)
			}
		}
	}
	plans.mu.Unlock()
	folder := os.Getenv("SITETRACK_ENDPOINT_OUTPUT")
	if folder == "" {
		folder = filepath.Join("..", "..", "bin", "endpoint-check")
	}
	if err := plans.write(filepath.Join(folder, "plans")); err != nil {
		t.Fatal(err)
	}
	results := make([]endpointResult, 0, len(h.results))
	for key, result := range h.results {
		for hash := range plans.routes[key] {
			result.Plans = append(result.Plans, hash)
		}
		sort.Strings(result.Plans)
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Path+results[i].Method < results[j].Path+results[j].Method })
	raw, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filepath.Join(folder, "endpoints.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified %d endpoints with %d SQL plans", len(results), len(plans.plans))
}

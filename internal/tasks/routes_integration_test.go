package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"sitetrack/internal/authentication"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/projects"
)

func token(t *testing.T, f *fixture, index int) string {
	t.Helper()
	pair, err := f.accounts.Login(context.Background(), authentication.LoginInput{Email: f.emails[index], Password: "A-secure-password-123"})
	if err != nil {
		t.Fatal(err)
	}
	return pair.AccessToken
}
func perform(router http.Handler, method, path, jwt, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if jwt != "" {
		r.Header.Set("Authorization", "Bearer "+jwt)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, r)
	return response
}
func TestTaskHTTPAccessValidationPaginationAndContracts(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "HTTP Tasks")
	root := f.create(t, p, "First Task")
	for range 24 {
		f.create(t, p, "Page Task")
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	contracts := openapi.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Register(router.Group(""), f.s, f.accounts, contracts, false, logger); err != nil {
		t.Fatal(err)
	}
	admin, outsider, super := token(t, f, 0), token(t, f, 1), token(t, f, 2)
	base := "/projects/" + p + "/tasks"
	if result := perform(router, "GET", base, "", ""); result.Code != 401 {
		t.Fatal("missing session accepted")
	}
	for _, path := range []string{base, base + "/" + root.ID, base + "/" + root.ID + "/dependencies", base + "/" + root.ID + "/relationships"} {
		denied := perform(router, "GET", path, outsider, "")
		missing := perform(router, "GET", strings.Replace(path, p, "PRJ-0000", 1), outsider, "")
		if denied.Code != 404 || denied.Body.String() != missing.Body.String() {
			t.Fatal("private project leaked", path, denied.Code)
		}
	}
	if result := perform(router, "GET", base, super, ""); result.Code != 200 {
		t.Fatal("superadmin cannot see tasks")
	}
	response := perform(router, "GET", base, admin, "")
	var page Page
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 20 || page.NextCursor == "" {
		t.Fatal("task default cursor page is wrong", response.Code, response.Body)
	}
	response = perform(router, "GET", base+"?cursor="+page.NextCursor, admin, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 5 {
		t.Fatal("task second cursor page is wrong", response.Body)
	}
	for _, query := range []string{"?limit=101", "?limit=0", "?cursor=bad", "?status=late", "?sort=unknown", "?late=invalid", "?from_date=2026-12-01&to_date=2026-01-01"} {
		if response = perform(router, "GET", base+query, admin, ""); response.Code != 400 {
			t.Fatal("invalid task query accepted", query, response.Code)
		}
	}
	for _, body := range []string{`{"name":"Bad Task","start_date":"2026-02-30","end_date":"2026-03-01"}`, `{"name":"Bad Task","start_date":"2026-03-01","end_date":"2026-02-01"}`, `{"name":"Bad Task","start_date":"2026-01-01","end_date":"2026-02-01","status":"done"}`, `{"name":"Bad Task","start_date":"2026-01-01","end_date":"2026-02-01","late":true}`} {
		if response = perform(router, "POST", base, admin, body); response.Code != 400 {
			t.Fatal("invalid or derived task input accepted", response.Code, response.Body)
		}
	}
	if response = perform(router, "POST", base+"/"+root.ID+"/done", admin, ""); response.Code != 409 {
		t.Fatal("illegal transition succeeded")
	}
	if _, err := f.s.pool.Exec(ctx, "INSERT INTO sitetrack.project_memberships(project_id,user_id,is_admin) VALUES($1,$2,false)", p, f.users[1]); err != nil {
		t.Fatal(err)
	}
	if response = perform(router, "POST", base+"/"+root.ID+"/start", outsider, ""); response.Code != 403 {
		t.Fatal("ordinary member started a task", response.Body)
	}
	if err := f.projects.ChangeMember(ctx, f.users[0], p, f.users[1], nil); err != nil {
		t.Fatal(err)
	}
	if response = perform(router, "GET", base+"/"+root.ID, outsider, ""); response.Code != 404 {
		t.Fatal("removed member retained task access")
	}
	raw, err := contracts.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc openapi.Document
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, methods := range doc.Paths {
		for _, op := range methods {
			for _, response := range op.Responses {
				for _, media := range response.Content {
					schema, err := media.Schema.Resolve(nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, example := range media.Examples {
						var value any
						if err = json.Unmarshal(example.Value, &value); err != nil {
							t.Fatal(err)
						}
						if err = schema.Validate(value); err != nil {
							t.Fatal("invalid task response contract", op.OperationID, err)
						}
					}
				}
			}
		}
	}
}
func TestTaskFiltersDependencyPaginationAndProjectSorts(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "Dependency Pages")
	target := f.create(t, p, "Many dependencies")
	for range 22 {
		source := f.create(t, p, "Dependency")
		f.link(t, p, target.ID, source.ID)
	}
	current, err := f.s.Get(ctx, f.users[0], p, target.ID)
	if err != nil || len(current.DependsOn) != 20 || current.DependencyCount != 22 || !current.HasMoreDependencies || !current.Blocked {
		t.Fatal("dependency preview is unbounded or incomplete", err)
	}
	deps, err := f.s.Dependencies(ctx, f.users[0], p, target.ID, DependencyListInput{Limit: 20})
	if err != nil || len(deps.Items) != 20 || deps.NextCursor == "" {
		t.Fatal("dependency pagination failed", err)
	}
	rest, err := f.s.Dependencies(ctx, f.users[0], p, target.ID, DependencyListInput{Limit: 20, Cursor: deps.NextCursor})
	if err != nil || len(rest.Items) != 2 {
		t.Fatal("dependency continuation failed", err)
	}
	blocked, late := true, true
	page, err := f.s.List(ctx, f.users[0], p, ListInput{Sort: "id", Order: "asc", Limit: 20, Blocked: &blocked, Late: &late, Status: "not_started", FromDate: "2000-01-15", ToDate: "2000-01-20"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != target.ID {
		t.Fatal("combined task filters are wrong", err)
	}
	page, err = f.s.List(ctx, f.users[0], p, ListInput{Sort: "id", Order: "asc", Limit: 20, Search: "%"})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("task search treated wildcard as syntax")
	}
	empty := f.project(t, "Empty Site")
	half := f.project(t, "Half Site")
	a := f.create(t, half, "Half A")
	f.create(t, half, "Half B")
	f.action(t, half, a.ID, "start")
	f.action(t, half, a.ID, "done")
	for _, sort := range []string{"completion_percentage", "late_count", "name", "start_date", "id"} {
		input := projects.ListInput{Sort: sort, Limit: 1}
		seen := map[string]bool{}
		for range 4 {
			page, err := f.projects.List(ctx, f.users[0], input)
			if err != nil {
				t.Fatal("project summary cursor failed", sort, err)
			}
			for _, row := range page.Items {
				if seen[row.ID] {
					t.Fatal("project cursor duplicated row")
				}
				seen[row.ID] = true
			}
			if page.NextCursor == "" {
				break
			}
			input.Cursor = page.NextCursor
		}
		if len(seen) != 3 {
			t.Fatal("project summary cursor skipped row", sort)
		}
	}
	visible, err := f.projects.List(ctx, f.users[0], projects.ListInput{Sort: "id", Limit: 20, LateOnly: true})
	if err != nil || len(visible.Items) != 2 {
		t.Fatal("late-only project filter is wrong", err)
	}
	for _, row := range visible.Items {
		if row.ID == empty {
			t.Fatal("empty project is late")
		}
	}
	denied, err := f.projects.List(ctx, f.users[1], projects.ListInput{Sort: "id", Limit: 20, LateOnly: true})
	if err != nil || len(denied.Items) != 0 {
		t.Fatal("project statistics leaked inaccessible rows")
	}
	if err = f.s.Remove(ctx, f.users[0], p, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Get(ctx, f.users[0], p, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed dependent task remains visible")
	}
	var edges int
	if err = f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.task_dependencies WHERE dependent_task_id=$1", target.ID).Scan(&edges); err != nil || edges != 0 {
		t.Fatal("removal left outgoing dependency rows")
	}
}

func TestTaskWritesRequireProjectAdminAndActionsFollowMembership(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "Task Permissions")
	a, b := f.create(t, p, "First Task"), f.create(t, p, "Second Task")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := openapi.New()
	if err := Register(router.Group(""), f.s, f.accounts, registry, false, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	admin, member, super := token(t, f, 0), token(t, f, 1), token(t, f, 2)
	base := "/projects/" + p + "/tasks"
	body := `{"name":"Updated Task","start_date":"2000-01-01","end_date":"2000-02-01"}`
	dependency := `{"required_task_id":"` + a.ID + `"}`
	writes := []struct{ method, path, body string }{
		{"POST", base, body}, {"PUT", base + "/" + a.ID, body}, {"DELETE", base + "/" + a.ID, ""},
		{"POST", base + "/" + a.ID + "/start", ""}, {"POST", base + "/" + a.ID + "/done", ""}, {"POST", base + "/" + a.ID + "/reopen", ""},
		{"POST", base + "/" + b.ID + "/dependencies", dependency}, {"DELETE", base + "/" + b.ID + "/dependencies/" + a.ID, ""},
	}
	for _, request := range writes {
		if response := perform(router, request.method, request.path, member, request.body); response.Code != 404 {
			t.Fatal("nonmember write exposed project", request.path, response.Code, response.Body)
		}
	}
	if _, err := f.s.pool.Exec(ctx, "INSERT INTO sitetrack.project_memberships(project_id,user_id,is_admin) VALUES($1,$2,false)", p, f.users[1]); err != nil {
		t.Fatal(err)
	}
	for _, jwt := range []string{member, super} {
		for _, request := range writes {
			if response := perform(router, request.method, request.path, jwt, request.body); response.Code != 403 {
				t.Fatal("non-admin write accepted", request.path, response.Code, response.Body)
			}
		}
		for _, path := range []string{base, base + "/" + a.ID, base + "/" + b.ID + "/dependencies", base + "/" + b.ID + "/relationships"} {
			if response := perform(router, "GET", path, jwt, ""); response.Code != 200 {
				t.Fatal("read rejected", path, response.Code, response.Body)
			}
		}
		response := perform(router, "GET", base+"/"+a.ID, jwt, "")
		var task Task
		if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil || len(task.AllowedActions) != 0 {
			t.Fatal("non-admin received actions", err, task.AllowedActions)
		}
		response = perform(router, "GET", base, jwt, "")
		var page Page
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, task := range page.Items {
			if len(task.AllowedActions) != 0 {
				t.Fatal("list exposed admin actions")
			}
		}
	}
	trueValue, falseValue := true, false
	if err := f.projects.ChangeMember(ctx, f.users[0], p, f.users[1], &trueValue); err != nil {
		t.Fatal(err)
	}
	response := perform(router, "GET", base+"/"+a.ID, member, "")
	var task Task
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil || len(task.AllowedActions) != 1 || task.AllowedActions[0] != "start" {
		t.Fatal("promotion did not update actions", err, task.AllowedActions)
	}
	if response := perform(router, "PUT", base+"/"+a.ID, member, body); response.Code != 200 {
		t.Fatal("promoted admin cannot edit task", response.Code, response.Body)
	}
	if err := f.projects.ChangeMember(ctx, f.users[0], p, f.users[1], &falseValue); err != nil {
		t.Fatal(err)
	}
	if response := perform(router, "POST", base+"/"+a.ID+"/start", member, ""); response.Code != 403 {
		t.Fatal("demoted admin kept write access", response.Code)
	}
	for _, step := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", base, body, 201}, {"PUT", base + "/" + a.ID, body, 200},
		{"POST", base + "/" + b.ID + "/dependencies", dependency, 201}, {"DELETE", base + "/" + b.ID + "/dependencies/" + a.ID, "", 204},
		{"POST", base + "/" + a.ID + "/start", "", 200}, {"POST", base + "/" + a.ID + "/done", "", 200},
		{"POST", base + "/" + a.ID + "/reopen", "", 200}, {"DELETE", base + "/" + a.ID, "", 204},
	} {
		if response := perform(router, step.method, step.path, admin, step.body); response.Code != step.status {
			t.Fatal("admin action failed", step.path, response.Code, response.Body)
		}
	}
}

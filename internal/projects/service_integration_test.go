package projects

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rahmannugar/authlier/password"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"sitetrack/internal/authentication"
	"sitetrack/internal/config"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/infra/openapi"
)

type fixture struct {
	s        *Service
	accounts *authentication.Service
	users    []uuid.UUID
	emails   []string
}

func projectTest(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, url, 15)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cancel()
	accounts, err := authentication.NewService(pool, config.Authentication{OTPSecret: []byte("12345678901234567890123456789012"), OutboxKey: []byte("abcdefghijklmnopqrstuvxyz1234567"), JWTSeed: []byte("12345678901234567890123456789012")})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: NewService(pool), accounts: accounts}
	hash, err := password.Hash("A-secure-password-123")
	if err != nil {
		t.Fatal(err)
	}
	seedContext, seedCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer seedCancel()
	for _, role := range []string{"user", "user", "superadmin", "company_store_manager"} {
		id := uuid.Must(uuid.NewV7())
		email := id.String() + "@example.test"
		if _, err = pool.Exec(seedContext, "INSERT INTO sitetrack.users(id,name,email,password_hash,role,email_verified) VALUES($1,'Site Engineer',$2,$3,$4,true)", id, email, hash, role); err != nil {
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

		rows, err := tx.Query(ctx, "SELECT DISTINCT project_id FROM sitetrack.project_memberships WHERE user_id=ANY($1)", f.users)
		if err != nil {
			t.Error(err)
			return
		}
		var projects []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				t.Error(err)
				break
			}
			projects = append(projects, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			t.Error(err)
			return
		}
		for _, statement := range []string{
			"UPDATE sitetrack.projects SET deleted_at=now() WHERE id=ANY($1)",
			"DELETE FROM sitetrack.notifications WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.project_invitations WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.project_memberships WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.projects WHERE id=ANY($1)",
		} {
			if _, err = tx.Exec(ctx, statement, projects); err != nil {
				t.Error(err)
				return
			}
		}

		if _, err = tx.Exec(ctx, "DELETE FROM sitetrack.users WHERE id=ANY($1)", f.users); err != nil {
			t.Error(err)
			return
		}
		if err = tx.Commit(ctx); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *fixture) create(t *testing.T, name string) Project {
	t.Helper()
	p, err := f.s.Create(context.Background(), f.users[0], Input{Name: name, StartDate: "2026-11-10", EndDate: "2027-02-10"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *fixture) add(t *testing.T, id string, index int, admin bool) {
	t.Helper()
	_, err := f.s.pool.Exec(context.Background(), "INSERT INTO sitetrack.project_memberships(project_id,user_id,is_admin) VALUES($1,$2,$3)", id, f.users[index], admin)
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectCreationNumberingAndRollback(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	created := make(chan Project, 12)
	failures := make(chan error, 12)
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			p, err := f.s.Create(ctx, f.users[0], Input{Name: "Concurrent Site", StartDate: "2026-11-10", EndDate: "2026-11-10"})
			if err != nil {
				failures <- err
			} else {
				created <- p
			}
		})
	}
	group.Wait()
	close(created)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	var first Project
	maxNumber := 0
	for p := range created {
		if ids[p.ID] || !p.IsAdmin || p.MemberCount != 1 {
			t.Fatalf("invalid atomic project: %+v", p)
		}
		ids[p.ID] = true
		first = p
		number, err := strconv.Atoi(strings.TrimPrefix(p.ID, "PRJ-"))
		if err != nil {
			t.Fatal(err)
		}
		maxNumber = max(maxNumber, number)
	}
	if len(ids) != 12 {
		t.Fatal("concurrent creation lost projects")
	}
	if err := f.s.Remove(ctx, f.users[0], first.ID); err != nil {
		t.Fatal(err)
	}
	next := f.create(t, "Next Site")
	number, _ := strconv.Atoi(strings.TrimPrefix(next.ID, "PRJ-"))
	if number <= maxNumber {
		t.Fatal("project code reused")
	}
	if _, err := f.s.Get(ctx, f.users[2], first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed project remains visible")
	}
	if _, err := f.s.Create(ctx, uuid.Must(uuid.NewV7()), Input{Name: "Rollback Site", StartDate: "2026-11-10", EndDate: "2027-02-10"}); err == nil {
		t.Fatal("nonexistent creator succeeded")
	}
	var count int
	if err := f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.projects WHERE name='Rollback Site'").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed creation left a project")
	}
	updated, err := f.s.Update(ctx, f.users[0], next.ID, Input{Name: "Renamed Site", StartDate: "2027-01-01", EndDate: "2027-01-30"})
	if err != nil || updated.ID != next.ID || updated.Name != "Renamed Site" || updated.StartDate != "2027-01-01" {
		t.Fatal("project update changed identity or lost details")
	}
	if _, err := f.s.Update(ctx, f.users[0], next.ID, Input{Name: "Bad Dates", StartDate: "2027-02-10", EndDate: "2026-11-10"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("reversed dates accepted")
	}
}

func TestProjectAccessAndMembershipChanges(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	p := f.create(t, "Private Site")
	for _, index := range []int{1, 3} {
		if _, err := f.s.Get(ctx, f.users[index], p.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("outsider read project")
		}
		if _, err := f.s.Members(ctx, f.users[index], p.ID, MemberListInput{Limit: 10}); !errors.Is(err, ErrNotFound) {
			t.Fatal("outsider read members")
		}
		if err := f.s.Remove(ctx, f.users[index], p.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("outsider removed project")
		}
		page, err := f.s.List(ctx, f.users[index], ListInput{Sort: "id", Limit: 10})
		if err != nil || len(page.Items) != 0 {
			t.Fatal("private project leaked in list")
		}
	}
	if visible, err := f.s.Get(ctx, f.users[2], p.ID); err != nil || visible.IsAdmin {
		t.Fatal("superadmin visibility/admin distinction failed")
	}
	admin := true
	if err := f.s.ChangeMember(ctx, f.users[2], p.ID, f.users[0], &admin); !errors.Is(err, ErrForbidden) {
		t.Fatal("superadmin bypassed project admin policy")
	}
	if err := f.s.ChangeMember(ctx, f.users[0], p.ID, f.users[1], &admin); !errors.Is(err, ErrNotFound) {
		t.Fatal("admin change added a member")
	}
	f.add(t, p.ID, 1, false)
	if _, err := f.s.Get(ctx, f.users[1], p.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ChangeMember(ctx, f.users[1], p.ID, f.users[0], nil); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary member changed membership")
	}
	if err := f.s.ChangeMember(ctx, f.users[0], p.ID, f.users[1], &admin); err != nil {
		t.Fatal(err)
	}
	for _, actor := range f.users[:2] {
		demote := false
		for _, value := range []*bool{nil, &demote} {
			if err := f.s.ChangeMember(ctx, actor, p.ID, actor, value); !errors.Is(err, ErrSelfMembership) {
				t.Fatalf("self membership change accepted: %v", err)
			}
		}
		current, err := f.s.Get(ctx, actor, p.ID)
		if err != nil || !current.IsAdmin {
			t.Fatal("rejected self change altered membership")
		}
	}
	if err := f.s.ChangeMember(ctx, f.users[1], p.ID, f.users[0], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.users[0], p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed member retained access")
	}
	if err := f.s.ChangeMember(ctx, f.users[1], p.ID, f.users[1], nil); !errors.Is(err, ErrSelfMembership) {
		t.Fatal("last admin removed")
	}
	if _, err := f.s.pool.Exec(ctx, "UPDATE sitetrack.users SET role='user' WHERE id=$1", f.users[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.users[2], p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("role downgrade retained global visibility")
	}
}

func TestConcurrentAdminChangesAndDatabaseConstraint(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	for _, remove := range []bool{false, true} {
		p := f.create(t, "Admin Race")
		f.add(t, p.ID, 1, true)
		start := make(chan struct{})
		results := make(chan error, 2)
		for index, actor := range f.users[:2] {
			target := f.users[1-index]
			go func(actor, target uuid.UUID) {
				<-start
				var value *bool
				if !remove {
					demote := false
					value = &demote
				}
				results <- f.s.ChangeMember(ctx, actor, p.ID, target, value)
			}(actor, target)
		}
		close(start)
		a, b := <-results, <-results
		denied := ErrForbidden
		if remove {
			denied = ErrNotFound
		}
		if !((a == nil && errors.Is(b, denied)) || (b == nil && errors.Is(a, denied))) {
			t.Fatalf("unexpected admin race: %v / %v", a, b)
		}
		var admins int
		if err := f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.project_memberships WHERE project_id=$1 AND is_admin", p.ID).Scan(&admins); err != nil || admins != 1 {
			t.Fatal("admin invariant violated")
		}
		if _, err := f.s.pool.Exec(ctx, "UPDATE sitetrack.project_memberships SET is_admin=false WHERE project_id=$1", p.ID); err == nil {
			t.Fatal("database accepted no-admin project")
		}
	}
	tx, err := f.s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.projects(id,name,start_date,end_date) VALUES('PRJ-999999999','No Admin','2026-11-10','2026-11-10')"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil {
		t.Fatal("database committed project without creator admin")
	}
}

func TestProjectPaginationScopesSearchAndStableTies(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	for range 5 {
		f.create(t, "Same Name")
	}
	for _, sort := range []string{"id", "name", "start_date"} {
		input := ListInput{Sort: sort, Search: "same", Limit: 2}
		seen := map[string]bool{}
		for range 4 {
			page, err := f.s.List(ctx, f.users[0], input)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range page.Items {
				if seen[p.ID] {
					t.Fatal("duplicate cursor item")
				}
				seen[p.ID] = true
			}
			if page.NextCursor == "" {
				break
			}
			input.Cursor = page.NextCursor
		}
		if len(seen) != 5 {
			t.Fatal("cursor skipped tied projects")
		}
	}
	page, err := f.s.List(ctx, f.users[0], ListInput{Sort: "id", Search: "%", Limit: 2})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("search interpreted wildcard")
	}
	page, err = f.s.List(ctx, f.users[0], ListInput{Sort: "id", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.List(ctx, f.users[0], ListInput{Sort: "name", Cursor: page.NextCursor, Limit: 2}); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor reused with different sort")
	}
	longName := strings.Repeat("界", 150)
	f.create(t, longName)
	f.create(t, longName)
	first, err := f.s.List(ctx, f.users[0], ListInput{Sort: "name", Search: longName, Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatal("long-name pagination failed")
	}
	second, err := f.s.List(ctx, f.users[0], ListInput{Sort: "name", Search: longName, Cursor: first.NextCursor, Limit: 1})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatal("generated Unicode cursor could not be reused")
	}

	p := f.create(t, "Team Pagination")
	f.add(t, p.ID, 1, false)
	f.add(t, p.ID, 2, false)
	members, err := f.s.Members(ctx, f.users[0], p.ID, MemberListInput{Limit: 2})
	if err != nil || len(members.Items) != 2 || members.NextCursor == "" {
		t.Fatal("members first page failed")
	}
	last, err := f.s.Members(ctx, f.users[0], p.ID, MemberListInput{Limit: 2, Cursor: members.NextCursor})
	if err != nil || len(last.Items) != 1 || last.NextCursor != "" {
		t.Fatal("members second page failed")
	}
}

func token(t *testing.T, f *fixture, index int) string {
	t.Helper()
	pair, err := f.accounts.Login(context.Background(), authentication.LoginInput{Email: f.emails[index], Password: "A-secure-password-123"})
	if err != nil {
		t.Fatal(err)
	}
	return pair.AccessToken
}
func perform(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}
func TestProjectHTTPContractsAndImmediateRevocation(t *testing.T) {
	f := projectTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	contracts := openapi.New()
	if err := Register(router.Group(""), f.s, f.accounts, contracts, false, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	adminToken, otherToken := token(t, f, 0), token(t, f, 1)
	w := perform(router, "POST", "/projects", adminToken, `{"name":"HTTP Site","start_date":"2026-11-10","end_date":"2027-02-10"}`)
	if w.Code != 201 {
		t.Fatalf("create HTTP %d: %s", w.Code, w.Body)
	}
	var p Project
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct{ method, body string }{
		{"PATCH", `{"is_admin":false}`}, {"DELETE", ""},
	} {
		w := perform(router, attempt.method, "/projects/"+p.ID+"/members/"+f.users[0].String(), adminToken, attempt.body)
		if w.Code != 409 || !strings.Contains(w.Body.String(), ErrSelfMembership.Error()) {
			t.Fatalf("self membership response: %d %s", w.Code, w.Body)
		}
	}
	for _, path := range []string{"/projects/" + p.ID, "/projects/" + p.ID + "/members"} {
		if w := perform(router, "GET", path, "", ""); w.Code != 401 {
			t.Fatal("missing JWT accepted")
		}
		actual := perform(router, "GET", path, otherToken, "")
		missing := perform(router, "GET", strings.Replace(path, p.ID, "PRJ-0000", 1), otherToken, "")
		if actual.Code != 404 || actual.Body.String() != missing.Body.String() {
			t.Fatal("project existence leaked")
		}
	}
	for _, query := range []string{"?limit=101", "?limit=-1", "?limit=2&limit=3", "?cursor=broken", "?unknown=x", "?sort=late", "?limit=2;bad=3"} {
		if w := perform(router, "GET", "/projects"+query, adminToken, ""); w.Code != 400 {
			t.Fatalf("accepted invalid query %s: %d", query, w.Code)
		}
	}
	f.add(t, p.ID, 1, false)
	if w := perform(router, "GET", "/projects/"+p.ID, otherToken, ""); w.Code != 200 {
		t.Fatal("member rejected")
	}
	for _, body := range []string{`{}`, `{"is_admin":null}`, `{"is_admin":"false"}`} {
		if w := perform(router, "PATCH", "/projects/"+p.ID+"/members/"+f.users[1].String(), adminToken, body); w.Code != 400 {
			t.Fatalf("accepted invalid admin input %s", body)
		}
	}
	if w := perform(router, "PATCH", "/projects/"+p.ID+"/members/"+f.users[1].String(), adminToken, `{"is_admin":false}`); w.Code != 204 {
		t.Fatalf("false admin value rejected: %s", w.Body)
	}
	if w := perform(router, "DELETE", "/projects/"+p.ID+"/members/"+f.users[1].String(), adminToken, ""); w.Code != 204 {
		t.Fatal("remove member failed")
	}
	if w := perform(router, "GET", "/projects/"+p.ID, otherToken, ""); w.Code != 404 {
		t.Fatal("JWT retained removed membership")
	}
	limits := gin.New()
	if err := Register(limits.Group(""), f.s, f.accounts, openapi.New(), true, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	for range createProjectRateLimit.Burst {
		if w := perform(limits, "POST", "/projects", "", ""); w.Code != 401 {
			t.Fatalf("unexpected unauthenticated limit status: %d", w.Code)
		}
	}
	if w := perform(limits, "POST", "/projects", "", ""); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("limiter did not protect authentication checks")
	}

	raw, err := contracts.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc openapi.Document
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Paths["/projects/{id}/members/{user_id}"]; !ok {
		t.Fatal("OpenAPI path parameters not normalized")
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
							t.Fatalf("response example invalid: %s: %v", op.OperationID, err)
						}
					}
				}
			}
		}
	}
}

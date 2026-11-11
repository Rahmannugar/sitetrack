package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rahmannugar/authlier/password"
	"github.com/google/uuid"
	"sitetrack/internal/authentication"
	"sitetrack/internal/config"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/projects"
)

type fixture struct {
	s        *Service
	projects *projects.Service
	accounts *authentication.Service
	users    []uuid.UUID
	emails   []string
	ids      []string
}

func taskTest(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	setup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pool, err := database.Open(setup, url, 15)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = database.Migrate(setup, pool); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	accounts, err := authentication.NewService(pool, config.Authentication{OTPSecret: []byte("12345678901234567890123456789012"), OutboxKey: []byte("abcdefghijklmnopqrstuvxyz1234567"), JWTSeed: []byte("12345678901234567890123456789012")})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := password.Hash("A-secure-password-123")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: NewService(pool), projects: projects.NewService(pool), accounts: accounts}
	seed, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	for _, role := range []string{"user", "company_store_manager", "superadmin"} {
		id := uuid.Must(uuid.NewV7())
		email := id.String() + "@example.test"
		if _, err = pool.Exec(seed, "INSERT INTO sitetrack.users(id,name,email,password_hash,role,email_verified) VALUES($1,'Task Engineer',$2,$3,$4,true)", id, email, hash, role); err != nil {
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
		for _, statement := range []string{
			"UPDATE sitetrack.projects SET deleted_at=now() WHERE id=ANY($1)",
			"DELETE FROM sitetrack.notifications WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.task_dependencies WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.tasks WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.project_invitations WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.project_stores WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.project_memberships WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.projects WHERE id=ANY($1)",
		} {
			if _, err = tx.Exec(ctx, statement, f.ids); err != nil {
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
func (f *fixture) project(t *testing.T, name string) string {
	t.Helper()
	p, err := f.projects.Create(context.Background(), f.users[0], projects.Input{Name: name, StartDate: "2026-11-10", EndDate: "2027-02-10"})
	if err != nil {
		t.Fatal(err)
	}
	f.ids = append(f.ids, p.ID)
	return p.ID
}
func (f *fixture) create(t *testing.T, project, name string) Task {
	t.Helper()
	task, err := f.s.Create(context.Background(), f.users[0], project, Input{Name: name, StartDate: "2000-01-01", EndDate: "2000-02-01"})
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func (f *fixture) action(t *testing.T, project, id, action string) Task {
	t.Helper()
	result, err := f.s.Transition(context.Background(), f.users[0], project, id, action)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func (f *fixture) link(t *testing.T, project, dependent, dependency string) Dependency {
	t.Helper()
	result, err := f.s.AddDependency(context.Background(), f.users[0], project, dependent, dependency)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestConcurrentTaskNumberingAndRemovalNeverReuseCodes(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "Numbered Site")
	results := make(chan Task, 12)
	failures := make(chan error, 12)
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			row, err := f.s.Create(ctx, f.users[0], p, Input{Name: "Concurrent task", StartDate: "2000-01-01", EndDate: "2000-02-01"})
			if err != nil {
				failures <- err
			} else {
				results <- row
			}
		})
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var first Task
	highest := 0
	for row := range results {
		if seen[row.ID] || row.ProjectID != p || !strings.HasPrefix(row.ID, p+"-T-") {
			t.Fatal("invalid task identity", row.ID)
		}
		seen[row.ID] = true
		first = row
		value, err := strconv.Atoi(strings.TrimPrefix(row.ID, p+"-T-"))
		if err != nil {
			t.Fatal(err)
		}
		highest = max(highest, value)
	}
	if len(seen) != 12 {
		t.Fatal("task creation lost rows")
	}
	if err := f.s.Remove(ctx, f.users[0], p, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.users[0], p, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed task remains visible")
	}
	next := f.create(t, p, "Next task")
	if next.ID != fmt.Sprintf("%s-T-%04d", p, highest+1) {
		t.Fatal("task number was reused", next.ID)
	}
	other := f.project(t, "Other numbered site")
	if row := f.create(t, other, "First elsewhere"); row.ID != other+"-T-0001" {
		t.Fatal("numbering is not per project")
	}
}
func TestChainTransitionsSynchronizeStoredDependenciesAndSummaries(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "Chain Site")
	a, b, c := f.create(t, p, "Task A"), f.create(t, p, "Task B"), f.create(t, p, "Task C")
	if f.link(t, p, b.ID, a.ID).Status != "waiting" || f.link(t, p, c.ID, b.ID).Status != "waiting" {
		t.Fatal("unfinished edge was satisfied")
	}
	if _, err := f.s.Transition(ctx, f.users[0], p, c.ID, "start"); !errors.Is(err, ErrConflict) {
		t.Fatal("blocked task started")
	}
	f.action(t, p, a.ID, "start")
	f.action(t, p, a.ID, "done")
	deps, err := f.s.Dependencies(ctx, f.users[0], p, b.ID, DependencyListInput{Limit: 20})
	if err != nil || len(deps.Items) != 1 || deps.Items[0].Status != "satisfied" {
		t.Fatal("completion did not persist dependency state", err)
	}
	f.action(t, p, b.ID, "start")
	if _, err = f.s.Transition(ctx, f.users[0], p, a.ID, "reopen"); !errors.Is(err, ErrConflict) {
		t.Fatal("dependency reopened under started dependent")
	}
	f.action(t, p, b.ID, "done")
	f.action(t, p, b.ID, "reopen")
	blocked, err := f.s.Get(ctx, f.users[0], p, c.ID)
	if err != nil || !blocked.Blocked || len(blocked.AllowedActions) != 0 {
		t.Fatal("reopening did not block dependent", err)
	}
	f.action(t, p, b.ID, "done")
	f.action(t, p, c.ID, "start")
	if _, err = f.s.Transition(ctx, f.users[0], p, b.ID, "reopen"); !errors.Is(err, ErrConflict) {
		t.Fatal("reopen ignored dependent state")
	}
	project, err := f.projects.Get(ctx, f.users[0], p)
	if err != nil || project.TaskCount != 3 || project.DoneCount != 2 || project.InProgressCount != 1 || project.CompletionPercentage != 66.67 || project.LateCount != 1 || project.BlockedCount != 0 {
		t.Fatal("incorrect project task summary", project, err)
	}
	current, err := f.s.Get(ctx, f.users[0], p, a.ID)
	if err != nil || current.Late || current.Blocked || len(current.AllowedActions) != 0 {
		t.Fatal("done task flags or actions are wrong")
	}
}
func TestDependencyGraphRejectsCyclesCrossProjectAndStartedEdits(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "Graph Site")
	a, b, c, d := f.create(t, p, "Task A"), f.create(t, p, "Task B"), f.create(t, p, "Task C"), f.create(t, p, "Task D")
	f.link(t, p, b.ID, a.ID)
	f.link(t, p, c.ID, b.ID)
	f.link(t, p, d.ID, a.ID)
	f.link(t, p, c.ID, d.ID)
	f.link(t, p, c.ID, a.ID)
	if _, err := f.s.AddDependency(ctx, f.users[0], p, a.ID, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("cycle accepted")
	}
	if _, err := f.s.AddDependency(ctx, f.users[0], p, c.ID, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("self link accepted")
	}
	if _, err := f.s.AddDependency(ctx, f.users[0], p, c.ID, a.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate dependency accepted")
	}
	other := f.project(t, "Other Graph")
	foreign := f.create(t, other, "Foreign Task")
	if _, err := f.s.AddDependency(ctx, f.users[0], p, c.ID, foreign.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-project dependency accepted")
	}
	page, err := f.s.Relationships(ctx, f.users[0], p, c.ID, RelationshipInput{Direction: "dependencies", Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatal("relationship page failed", err)
	}
	all := append([]Relationship{}, page.Items...)
	last, err := f.s.Relationships(ctx, f.users[0], p, c.ID, RelationshipInput{Direction: "dependencies", Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	all = append(all, last.Items...)
	if len(all) != 3 {
		t.Fatal("diamond paths duplicated nodes")
	}
	for _, row := range all {
		if row.Steps != 1 {
			t.Fatal("shortest path ignored direct shortcut")
		}
	}
	reverse, err := f.s.Relationships(ctx, f.users[0], p, a.ID, RelationshipInput{Direction: "dependents", Limit: 20})
	if err != nil || len(reverse.Items) != 3 {
		t.Fatal("reverse reachability failed", err)
	}
	if err = f.s.Remove(ctx, f.users[0], p, a.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("referenced task removed")
	}
	fresh := f.create(t, p, "Started Task")
	f.action(t, p, fresh.ID, "start")
	if _, err = f.s.AddDependency(ctx, f.users[0], p, fresh.ID, a.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("started task gained unfinished dependency")
	}
	f.action(t, p, a.ID, "start")
	f.action(t, p, a.ID, "done")
	if edge, err := f.s.AddDependency(ctx, f.users[0], p, fresh.ID, a.ID); err != nil || edge.Status != "satisfied" {
		t.Fatal("completed dependency could not be linked", err)
	}
	if err = f.s.RemoveDependency(ctx, f.users[0], p, c.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	allPaths, err := f.s.Relationships(ctx, f.users[0], p, c.ID, RelationshipInput{Direction: "dependencies", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range allPaths.Items {
		if row.ID == a.ID && row.Steps != 2 {
			t.Fatal("indirect shortest distance is wrong")
		}
	}
	if _, err = f.s.pool.Exec(ctx, "UPDATE sitetrack.task_dependencies SET status='waiting' WHERE required_task_id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = f.s.pool.QueryRow(ctx, "SELECT status FROM sitetrack.task_dependencies WHERE dependent_task_id=$1 AND required_task_id=$2", b.ID, a.ID).Scan(&status); err != nil || status != "satisfied" {
		t.Fatal("stored dependency state could be forged")
	}
}
func TestConcurrentGraphAndTransitionMutationsRemainConsistent(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "Race Site")
	race := func(first, second func() error) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		for _, run := range []func() error{first, second} {
			group.Go(func() { <-start; results <- run() })
		}
		close(start)
		group.Wait()
		a, b := <-results, <-results
		if !((a == nil && errors.Is(b, ErrConflict)) || (b == nil && errors.Is(a, ErrConflict))) {
			t.Fatalf("race produced invalid outcomes: %v / %v", a, b)
		}
	}
	a, b := f.create(t, p, "Cycle A"), f.create(t, p, "Cycle B")
	race(func() error { _, err := f.s.AddDependency(ctx, f.users[0], p, a.ID, b.ID); return err }, func() error { _, err := f.s.AddDependency(ctx, f.users[0], p, b.ID, a.ID); return err })
	root, leaf := f.create(t, p, "Dependency"), f.create(t, p, "Dependent")
	f.link(t, p, leaf.ID, root.ID)
	f.action(t, p, root.ID, "start")
	f.action(t, p, root.ID, "done")
	race(func() error { _, err := f.s.Transition(ctx, f.users[0], p, root.ID, "reopen"); return err }, func() error { _, err := f.s.Transition(ctx, f.users[0], p, leaf.ID, "start"); return err })
	var waitingStarted int
	if err := f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.task_dependencies d JOIN sitetrack.tasks t ON t.id=d.dependent_task_id WHERE d.project_id=$1 AND d.status='waiting' AND t.status<>'not_started'", p).Scan(&waitingStarted); err != nil || waitingStarted != 0 {
		t.Fatal("race left an invalid task state")
	}
	plain := f.create(t, p, "Single Start")
	race(func() error { _, err := f.s.Transition(ctx, f.users[0], p, plain.ID, "start"); return err }, func() error { _, err := f.s.Transition(ctx, f.users[0], p, plain.ID, "start"); return err })
}
func TestTaskMutationRollsBackWhenNotificationInsertFails(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "Rollback Site")
	if _, err := f.s.pool.Exec(ctx, "INSERT INTO sitetrack.project_memberships(project_id,user_id,is_admin) VALUES($1,$2,false)", p, f.users[1]); err != nil {
		t.Fatal(err)
	}
	a, b := f.create(t, p, "Dependency"), f.create(t, p, "Dependent")
	f.link(t, p, b.ID, a.ID)
	f.action(t, p, a.ID, "start")
	if _, err := f.s.pool.Exec(ctx, `CREATE FUNCTION sitetrack.reject_task_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced task notification failure'; END $$; CREATE TRIGGER reject_task_notice BEFORE INSERT ON sitetrack.notifications FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_task_notice();`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.s.pool.Exec(ctx, "DROP TRIGGER reject_task_notice ON sitetrack.notifications; DROP FUNCTION sitetrack.reject_task_notice()"); err != nil {
			t.Error(err)
		}
	}()
	if _, err := f.s.Transition(ctx, f.users[0], p, a.ID, "done"); err == nil {
		t.Fatal("task completion ignored notification failure")
	}
	current, err := f.s.Get(ctx, f.users[0], p, a.ID)
	if err != nil || current.Status != "in_progress" {
		t.Fatal("failed completion committed task status")
	}
	deps, err := f.s.Dependencies(ctx, f.users[0], p, b.ID, DependencyListInput{Limit: 20})
	if err != nil || deps.Items[0].Status != "waiting" {
		t.Fatal("failed completion committed dependency status")
	}
	if _, err = f.s.Create(ctx, f.users[0], p, Input{Name: "Failed Creation", StartDate: "2000-01-01", EndDate: "2000-02-01"}); err == nil {
		t.Fatal("task creation ignored notification failure")
	}
	var count int
	if err = f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.tasks WHERE project_id=$1", p).Scan(&count); err != nil || count != 2 {
		t.Fatal("failed task creation left a row")
	}
}
func TestTaskDateFlagsAndCursorOrdering(t *testing.T) {
	f := taskTest(t)
	ctx := context.Background()
	p := f.project(t, "Pagination Site")
	for range 25 {
		f.create(t, p, strings.Repeat("界", 150))
	}
	for _, sort := range []string{"id", "name", "start_date", "end_date", "dependent_count"} {
		for _, order := range []string{"asc", "desc"} {
			input := ListInput{Sort: sort, Order: order, Limit: 3}
			seen := map[string]bool{}
			for range 10 {
				page, err := f.s.List(ctx, f.users[0], p, input)
				if err != nil {
					t.Fatal(sort, order, err)
				}
				for _, task := range page.Items {
					if seen[task.ID] {
						t.Fatal("cursor duplicated a task")
					}
					seen[task.ID] = true
				}
				if page.NextCursor == "" {
					break
				}
				input.Cursor = page.NextCursor
			}
			if len(seen) != 25 {
				t.Fatal("cursor skipped tasks", sort, order, len(seen))
			}
		}
	}
	page, err := f.s.List(ctx, f.users[0], p, ListInput{Sort: "id", Order: "asc", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.List(ctx, f.users[0], p, ListInput{Sort: "name", Order: "asc", Limit: 2, Cursor: page.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor accepted different filters")
	}
	f.s.now = func() time.Time { return time.Date(2026, 11, 11, 0, 30, 0, 0, time.FixedZone("Lagos", 3600)) }
	dated, err := f.s.Create(ctx, f.users[0], p, Input{Name: "UTC boundary", StartDate: "2026-11-10", EndDate: "2026-11-11"})
	if err != nil || dated.Late {
		t.Fatal("date flags did not use UTC", err)
	}
	f.s.now = func() time.Time { return time.Date(2026, 11, 11, 0, 1, 0, 0, time.UTC) }
	dated, err = f.s.Get(ctx, f.users[0], p, dated.ID)
	if err != nil || !dated.Late {
		t.Fatal("past planned start was not late")
	}
	dated = f.action(t, p, dated.ID, "start")
	if dated.Late || !slices.Equal(dated.AllowedActions, []string{"done"}) {
		t.Fatal("late/action flags did not follow transition")
	}
}

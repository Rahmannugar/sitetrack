package database

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/tern/v2/migrate"
)

func TestTransactionalNotificationMigrationPreservesQueuedAndExistingRows(t *testing.T) {
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := Open(ctx, url, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	migrator, err := migrate.NewMigrator(ctx, connection.Conn(), "public.schema_version")
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err = migrator.LoadMigrations(files); err != nil {
		t.Fatal(err)
	}
	if err = migrator.MigrateTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	actor, invitee := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	invitation, existing, published, pending := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var number int64
	if err = connection.QueryRow(ctx, "SELECT nextval('sitetrack.project_number_seq')").Scan(&number); err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("PRJ-%04d", number)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := migrator.MigrateTo(cleanup, 7); err != nil {
			t.Error(err)
			return
		}
		tx, err := connection.Begin(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback(cleanup)
		for _, statement := range []string{
			"UPDATE sitetrack.projects SET deleted_at=now() WHERE id=$1",
			"DELETE FROM sitetrack.notifications WHERE project_id=$1",
			"DELETE FROM sitetrack.outbox_events WHERE aggregate_id=$1",
			"DELETE FROM sitetrack.project_invitations WHERE project_id=$1",
			"DELETE FROM sitetrack.project_stores WHERE project_id=$1",
			"DELETE FROM sitetrack.project_memberships WHERE project_id=$1",
			"DELETE FROM sitetrack.projects WHERE id=$1",
		} {
			if _, err = tx.Exec(cleanup, statement, project); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err = tx.Exec(cleanup, "DELETE FROM sitetrack.users WHERE id=ANY($1)", []uuid.UUID{actor, invitee}); err != nil {
			t.Error(err)
			return
		}
		if err = tx.Commit(cleanup); err != nil {
			t.Error(err)
		}
	}()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, id := range []uuid.UUID{actor, invitee} {
		if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.users(id,name,email,password_hash,role,email_verified) VALUES($1,'Migration User',$2,'fixture-hash','user',true)", id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.projects(id,name,start_date,end_date) VALUES($1,'Migration Site',current_date,current_date)", project); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.project_stores(id,project_id,name) VALUES($1,$2,'Migration store')", uuid.Must(uuid.NewV7()), project); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.project_memberships(project_id,user_id,is_admin) VALUES($1,$2,true)", project, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.project_invitations(id,project_id,inviter_id,invitee_id) VALUES($1,$2,$3,$4)", invitation, project, actor, invitee); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.outbox_events(id,aggregate_id,event_type,status,published_at) VALUES($1,$2,'notification.project','published',now())", published, project); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.notifications(id,event_id,user_id,project_id,type,title,message,read_at) VALUES($1,$2,$3,$4,'project.updated','Existing notification','Keep me',now())", existing, published, actor, project); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"project_id": project, "actor_id": actor.String(), "invitation_id": invitation.String(), "kind": "project.invitation", "title": "Queued invitation", "message": "Deliver me"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.outbox_events(id,aggregate_id,event_type,payload) VALUES($1,$2,'notification.project',$3)", pending, project, payload); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO sitetrack.outbox_recipients(event_id,user_id) VALUES($1,$2)", pending, invitee); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		var count int
		var read bool
		var id uuid.UUID
		if err = connection.QueryRow(ctx, "SELECT count(*) FROM sitetrack.notifications WHERE project_id=$1", project).Scan(&count); err != nil || count != 2 {
			t.Fatal("migration lost or duplicated notifications", count, err)
		}
		if err = connection.QueryRow(ctx, "SELECT read_at IS NOT NULL FROM sitetrack.notifications WHERE id=$1 AND title='Existing notification'", existing).Scan(&read); err != nil || !read {
			t.Fatal("existing notification identity/read state changed", err)
		}
		if err = connection.QueryRow(ctx, "SELECT id FROM sitetrack.notifications WHERE invitation_id=$1 AND title='Queued invitation'", invitation).Scan(&id); err != nil || id.Version() != 7 {
			t.Fatal("pending notification was not preserved with UUID v7", err)
		}
		if err = connection.QueryRow(ctx, "SELECT count(*) FROM sitetrack.outbox_events WHERE event_type='notification.project' AND aggregate_id=$1", project).Scan(&count); err != nil || count != 0 {
			t.Fatal("notification jobs remain in outbox", err)
		}
	}
	if err = migrator.MigrateTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	check()
	if err = migrator.MigrateTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	if err = migrator.MigrateTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	check()
}

package database

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/tern/v2/migrate"
)

func TestProjectInventoryMigrationPreservesStockAndDeliveries(t *testing.T) {
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
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	migrator, err := migrate.NewMigrator(ctx, conn.Conn(), "public.schema_version")
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.LoadMigrations(files); err != nil {
		t.Fatal(err)
	}
	if err := migrator.MigrateTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	actor, category := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	materials := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	stores := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	deliveries := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	var projects []string
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := migrator.Migrate(cleanup); err != nil {
			t.Error(err)
			return
		}
		tx, err := conn.Begin(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback(cleanup)
		for _, sql := range []string{
			"DELETE FROM sitetrack.delivery_items WHERE delivery_id IN (SELECT id FROM sitetrack.deliveries WHERE project_id=ANY($1))",
			"DELETE FROM sitetrack.deliveries WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.project_inventory WHERE project_id=ANY($1)",
			"UPDATE sitetrack.projects SET deleted_at=now() WHERE id=ANY($1)",
			"DELETE FROM sitetrack.project_memberships WHERE project_id=ANY($1)",
			"DELETE FROM sitetrack.projects WHERE id=ANY($1)",
		} {
			if _, err := tx.Exec(cleanup, sql, projects); err != nil {
				t.Error(err)
				return
			}
		}
		for _, sql := range []string{"DELETE FROM sitetrack.company_inventory WHERE material_id=ANY($1)", "DELETE FROM sitetrack.materials WHERE id=ANY($1)"} {
			if _, err := tx.Exec(cleanup, sql, materials); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := tx.Exec(cleanup, "DELETE FROM sitetrack.material_categories WHERE id=$1", category); err != nil {
			t.Error(err)
			return
		}
		if _, err := tx.Exec(cleanup, "DELETE FROM sitetrack.users WHERE id=$1", actor); err != nil {
			t.Error(err)
			return
		}
		if err := tx.Commit(cleanup); err != nil {
			t.Error(err)
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO sitetrack.users(id,name,email,password_hash,role,email_verified) VALUES($1,'Migration Admin',$2,'fixture-hash','company_store_manager',true)", actor, actor.String()+"@example.test")
	for i := range stores {
		var number int64
		if err := tx.QueryRow(ctx, "SELECT nextval('sitetrack.project_number_seq')").Scan(&number); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("PRJ-%04d", number)
		projects = append(projects, id)
		exec("INSERT INTO sitetrack.projects(id,name,start_date,end_date) VALUES($1,'Migration Site',current_date,current_date)", id)
		exec("INSERT INTO sitetrack.project_stores(id,project_id,name) VALUES($1,$2,'Migration store')", stores[i], id)
		exec("INSERT INTO sitetrack.project_memberships(project_id,user_id,is_admin) VALUES($1,$2,true)", id, actor)
	}
	exec("INSERT INTO sitetrack.material_categories(id,name) VALUES($1,$2)", category, category.String())
	for _, id := range materials {
		exec("INSERT INTO sitetrack.materials(id,name,category_id,unit) VALUES($1,$2,$3,'bag')", id, id.String(), category)
	}
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	exec("INSERT INTO sitetrack.company_inventory(material_id,quantity,in_transit_to_projects) VALUES($1,2,0.250),($2,0.500,0)", materials[0], materials[1])
	exec("INSERT INTO sitetrack.project_inventory(project_store_id,material_id,quantity,in_transit,last_delivery_at,created_at,updated_at) VALUES($1,$2,1.125,0.125,$3,$3,$3),($1,$4,0,0,$3,$3,$3),($5,$2,0,0,NULL,$3,$3)", stores[0], materials[0], at, materials[1], stores[1])
	for i, id := range deliveries {
		project, direction, material, amount := projects[0], "project_to_company", materials[0], "0.125"
		if i == 1 {
			project, direction, amount = projects[1], "company_to_project", "0.250"
		}
		if i == 2 {
			direction, material, amount = "company_to_project", materials[1], "0.500"
			exec("INSERT INTO sitetrack.deliveries(id,project_id,direction,status,initiated_by_user_id,completed_by_user_id,created_at,completed_at) VALUES($1,$2,$3,'cancelled',$4,$4,$5,$6)", id, project, direction, actor, at, at.Add(time.Hour))
		} else {
			exec("INSERT INTO sitetrack.deliveries(id,project_id,direction,initiated_by_user_id,created_at) VALUES($1,$2,$3,$4,$5)", id, project, direction, actor, at)
		}
		exec("INSERT INTO sitetrack.delivery_items(delivery_id,material_id,quantity) VALUES($1,$2,$3::text::numeric)", id, material, amount)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := func(version int32) string {
		t.Helper()
		inventory := `SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY i.project_id,i.material_id),'[]'::jsonb)::text FROM sitetrack.project_inventory i WHERE i.project_id=ANY($1)`
		if version == 14 {
			inventory = `SELECT COALESCE(jsonb_agg((to_jsonb(i)-'project_store_id') || jsonb_build_object('project_id',s.project_id) ORDER BY s.project_id,i.material_id),'[]'::jsonb)::text FROM sitetrack.project_inventory i JOIN sitetrack.project_stores s ON s.id=i.project_store_id WHERE s.project_id=ANY($1)`
		}
		statements := []string{inventory,
			"SELECT COALESCE(jsonb_agg(to_jsonb(d) ORDER BY d.id),'[]'::jsonb)::text FROM sitetrack.deliveries d WHERE d.project_id=ANY($1)",
			"SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY i.delivery_id,i.material_id),'[]'::jsonb)::text FROM sitetrack.delivery_items i JOIN sitetrack.deliveries d ON d.id=i.delivery_id WHERE d.project_id=ANY($1)",
		}
		values := make([]string, len(statements))
		for i, sql := range statements {
			if err := conn.QueryRow(ctx, sql, projects).Scan(&values[i]); err != nil {
				t.Fatal(err)
			}
		}
		return strings.Join(values, "\n")
	}
	before := snapshot(14)
	for _, version := range []int32{15, 14, 15} {
		if err := migrator.MigrateTo(ctx, version); err != nil {
			t.Fatal(err)
		}
		if snapshot(version) != before {
			t.Fatal("migration changed stock, transit, timestamps or delivery history", version)
		}
		if version != 15 {
			continue
		}
		var removed bool
		if err := conn.QueryRow(ctx, "SELECT to_regclass('sitetrack.project_stores') IS NULL").Scan(&removed); err != nil || !removed {
			t.Fatal("obsolete store table remains", err)
		}
		if _, err := conn.Exec(ctx, "INSERT INTO sitetrack.project_inventory(project_id,material_id) VALUES($1,$2)", projects[0], materials[0]); err == nil {
			t.Fatal("duplicate project/material accepted")
		}
		for _, id := range projects[:2] {
			if _, err := conn.Exec(ctx, "UPDATE sitetrack.projects SET deleted_at=now() WHERE id=$1", id); err == nil {
				t.Fatal("stock or incoming delivery stopped protecting project")
			}
		}
		if _, err := conn.Exec(ctx, "UPDATE sitetrack.materials SET unit='kg' WHERE id=$1", materials[0]); err == nil {
			t.Fatal("unit history protection lost")
		}
	}
}

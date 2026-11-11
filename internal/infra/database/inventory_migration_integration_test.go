package database

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/tern/v2/migrate"
)

func TestCompanyInventoryMigrationPreservesStock(t *testing.T) {
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
	if err := migrator.LoadMigrations(files); err != nil {
		t.Fatal(err)
	}
	if err := migrator.MigrateTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	category, material, empty := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := migrator.Migrate(cleanup); err != nil {
			t.Error(err)
			return
		}
		for _, statement := range []string{
			"DELETE FROM sitetrack.company_inventory WHERE material_id IN (SELECT id FROM sitetrack.materials WHERE category_id=$1)",
			"DELETE FROM sitetrack.materials WHERE category_id=$1",
			"DELETE FROM sitetrack.material_categories WHERE id=$1",
		} {
			if _, err := connection.Exec(cleanup, statement, category); err != nil {
				t.Error(err)
			}
		}
	}()
	if _, err := connection.Exec(ctx, "INSERT INTO sitetrack.company_stores(id,name) VALUES($1,'Company store') ON CONFLICT DO NOTHING", uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, "INSERT INTO sitetrack.material_categories(id,name) VALUES($1,$2)", category, category.String()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{material, empty} {
		if _, err := connection.Exec(ctx, "INSERT INTO sitetrack.materials(id,name,category_id,unit) VALUES($1,$2,$3,'bag')", id, id.String(), category); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	if _, err := connection.Exec(ctx, `INSERT INTO sitetrack.company_inventory(company_store_id,material_id,quantity,reorder_point,in_transit_to_projects,created_at,updated_at) SELECT id,$1,12.125,20.500,2.001,$2,$2 FROM sitetrack.company_stores`, material, at); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int32{13, 12, 13} {
		if err := migrator.MigrateTo(ctx, version); err != nil {
			t.Fatal(err)
		}
		var quantity, reorder, transit string
		var created, updated time.Time
		if err := connection.QueryRow(ctx, "SELECT quantity::text,reorder_point::text,in_transit_to_projects::text,created_at,updated_at FROM sitetrack.company_inventory WHERE material_id=$1", material).Scan(&quantity, &reorder, &transit, &created, &updated); err != nil {
			t.Fatal(err)
		}
		if quantity != "12.125" || reorder != "20.500" || transit != "2.001" || !created.Equal(at) || !updated.Equal(at) {
			t.Fatal("migration changed inventory values", version)
		}
		if version == 13 {
			var emptyQuantity, status string
			if err := connection.QueryRow(ctx, "SELECT quantity::text,status::text FROM sitetrack.company_stock WHERE material_id=$1", empty).Scan(&emptyQuantity, &status); err != nil {
				t.Fatal(err)
			}
			if emptyQuantity != "0.000" || status != "out_of_stock" {
				t.Fatal("unstocked catalogue material lost")
			}
			if _, err := connection.Exec(ctx, "INSERT INTO sitetrack.company_inventory(material_id) VALUES($1)", material); err == nil {
				t.Fatal("duplicate inventory material accepted")
			}
		}
	}
}

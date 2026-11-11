package seed

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

//go:embed reconcile.sql
var reconcileSQL string

//go:embed checks.sql
var checksSQL string

func (p *plan) validate(ctx context.Context, tx pgx.Tx) (Report, error) {
	expected := map[string]int{"users": p.profile.Users, "projects": p.profile.Projects, "tasks": p.profile.Tasks, "task_dependencies": p.profile.Dependencies, "materials": p.profile.Materials, "material_categories": p.profile.Categories, "project_invitations": p.profile.Invitations, "deliveries": p.profile.Deliveries, "delivery_items": p.profile.DeliveryItems}
	report := Report{Profile: p.profile.Name, AsOf: p.at, Counts: map[string]int64{}, Projects: append([]string(nil), p.projects[:min(3, len(p.projects))]...), Materials: append([]uuid.UUID(nil), p.materials[:min(3, len(p.materials))]...)}
	for _, table := range []string{"users", "projects", "project_memberships", "tasks", "task_dependencies", "material_categories", "materials", "project_invitations", "notifications", "deliveries", "delivery_items", "company_store", "project_store"} {
		var count int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{"sitetrack", table}.Sanitize()).Scan(&count); err != nil {
			return Report{}, safeError("count "+table, err)
		}
		report.Counts[table] = count
		if want, ok := expected[table]; ok && count != int64(want) {
			return Report{}, fmt.Errorf("seed count mismatch for %s: got %d, expected %d", table, count, want)
		}
	}
	for _, section := range strings.Split(checksSQL, "-- check: ")[1:] {
		name, query, _ := strings.Cut(section, "\n")
		var holds bool
		if err := tx.QueryRow(ctx, query).Scan(&holds); err != nil {
			return Report{}, safeError("check "+name, err)
		}
		if !holds {
			return Report{}, fmt.Errorf("seed invariant failed: %s", name)
		}
	}
	for _, table := range []string{"users", "material_categories", "materials", "project_invitations", "deliveries", "notifications"} {
		var valid bool
		if err := tx.QueryRow(ctx, "SELECT NOT EXISTS(SELECT 1 FROM "+pgx.Identifier{"sitetrack", table}.Sanitize()+" WHERE (get_byte(uuid_send(id),6)>>4)<>7)").Scan(&valid); err != nil {
			return Report{}, safeError("check UUIDs", err)
		}
		if !valid {
			return Report{}, fmt.Errorf("seed UUID version mismatch in %s", table)
		}
	}
	for i := range min(5, len(p.users)) {
		report.Accounts = append(report.Accounts, Account{ID: p.users[i], Email: email(i), Role: role(i)})
	}
	for i, id := range p.deliveries {
		m := p.movement(i)
		if m.status != "in_transit" {
			continue
		}
		if m.direction == "company_to_project" && report.PendingCompanyDelivery == uuid.Nil {
			report.PendingCompanyDelivery = id
		}
		if m.direction == "project_to_company" && report.PendingProjectDelivery == uuid.Nil {
			report.PendingProjectDelivery = id
		}
		if report.PendingCompanyDelivery != uuid.Nil && report.PendingProjectDelivery != uuid.Nil {
			break
		}
	}
	return report, nil
}

package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type copyData func(string, []string, int, func(int) ([]any, error)) error

func (p *plan) populate(ctx context.Context, tx pgx.Tx, hash string, copyRows copyData) error {
	if err := copyRows("users", []string{"id", "name", "email", "password_hash", "role", "email_verified", "created_at"}, p.profile.Users, func(i int) ([]any, error) {
		id, err := uuid.NewV7()
		p.users[i] = id
		return []any{id, fmt.Sprintf("Site Engineer %04d", i), email(i), hash, role(i), true, p.at.AddDate(-4, 0, 0)}, err
	}); err != nil {
		return err
	}
	if err := copyRows("projects", []string{"id", "name", "start_date", "end_date", "created_at", "task_number"}, p.profile.Projects, func(i int) ([]any, error) {
		names := []string{"Riverside Apartments", "Marina Warehouse", "North Bridge", "Hillview School", "Central Clinic", "Airport Access Road"}
		end := p.at.AddDate(0, 0, 90)
		if i%10 == 9 {
			end = p.at.AddDate(0, 0, -1)
		}
		return []any{p.projects[i], fmt.Sprintf("%s Phase %04d", names[i%len(names)], i+1), date(p.projectStart(i)), date(end), p.projectStart(i).Add(-24 * time.Hour), p.taskCounts[i]}, nil
	}); err != nil {
		return err
	}
	memberCount := 0
	for _, team := range p.teams {
		memberCount += len(team)
	}
	project, member := 0, 0
	if err := copyRows("project_memberships", []string{"project_id", "user_id", "is_admin", "joined_at"}, memberCount, func(_ int) ([]any, error) {
		at := p.projectStart(project).Add(24 * time.Hour)
		if member == 0 {
			at = p.projectStart(project).Add(-24 * time.Hour)
		}
		row := []any{p.projects[project], p.users[p.teams[project][member]], member == 0, at}
		member++
		if member == len(p.teams[project]) {
			project++
			member = 0
		}
		return row, nil
	}); err != nil {
		return err
	}
	categories := []string{"Cement", "Aggregates", "Reinforcement", "Timber", "Plumbing", "Electrical", "Roofing", "Paint", "Masonry", "Flooring", "Drainage", "Steelwork", "Fasteners", "Insulation", "Glazing", "Doors", "Waterproofing", "Safety", "Earthworks", "Finishes"}
	if err := copyRows("material_categories", []string{"id", "name", "created_at", "updated_at"}, p.profile.Categories, func(i int) ([]any, error) {
		id, err := uuid.NewV7()
		p.categories[i] = id
		at := p.at.AddDate(-4, 0, 1)
		return []any{id, fmt.Sprintf("%s %02d", categories[i%len(categories)], i/len(categories)+1), at, at}, err
	}); err != nil {
		return err
	}
	if err := copyRows("materials", []string{"id", "name", "category_id", "unit", "created_at", "updated_at"}, p.profile.Materials, func(i int) ([]any, error) {
		id, err := uuid.NewV7()
		p.materials[i] = id
		category := i % p.profile.Categories
		units := []string{"bag", "tonne", "piece", "metre", "piece", "metre", "sheet", "litre"}
		at := p.at.AddDate(-4, 0, 2)
		return []any{id, fmt.Sprintf("%s construction supply - SKU %05d", categories[category%len(categories)], i+1), p.categories[category], units[category%len(units)], at, at}, err
	}); err != nil {
		return err
	}
	// We import final task states under exclusive table locks, then validate every graph/status invariant.
	if _, err := tx.Exec(ctx, "ALTER TABLE sitetrack.tasks DISABLE TRIGGER guard_task; ALTER TABLE sitetrack.task_dependencies DISABLE TRIGGER guard_dependency"); err != nil {
		return safeError("prepare task import", err)
	}
	if err := copyRows("tasks", []string{"id", "project_id", "name", "start_date", "end_date", "status", "created_at"}, p.profile.Tasks, func(i int) ([]any, error) {
		project, number := p.taskPosition(i)
		start, end := p.taskDates(project, number)
		names := []string{"Survey site", "Excavate foundation", "Fix reinforcement", "Pour slab", "Install plumbing", "Install electrical conduits", "Build blockwork", "Apply finishes"}
		return []any{taskCode(p.projects[project], number), p.projects[project], fmt.Sprintf("%s - section %04d", names[(number-1)%len(names)], number), parsedDate(start), parsedDate(end), p.taskState(project, number), p.projectStart(project)}, nil
	}); err != nil {
		return err
	}
	project, number, edge := 0, 4, 0
	if err := copyRows("task_dependencies", []string{"project_id", "dependent_task_id", "required_task_id", "status"}, p.profile.Dependencies, func(_ int) ([]any, error) {
		required := p.required(project, number)
		state := "waiting"
		if p.taskState(project, required[edge]) == "done" {
			state = "satisfied"
		}
		row := []any{p.projects[project], taskCode(p.projects[project], number), taskCode(p.projects[project], required[edge]), state}
		edge++
		if edge == len(required) {
			edge = 0
			number++
			if number > p.taskCounts[project] {
				project++
				number = 4
			}
		}
		return row, nil
	}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "ALTER TABLE sitetrack.tasks ENABLE TRIGGER guard_task; ALTER TABLE sitetrack.task_dependencies ENABLE TRIGGER guard_dependency"); err != nil {
		return safeError("restore task guards", err)
	}
	if err := copyRows("project_invitations", []string{"id", "project_id", "inviter_id", "invitee_id", "status", "created_at", "completed_at"}, p.profile.Invitations, func(i int) ([]any, error) {
		id, err := uuid.NewV7()
		p.invitations[i] = id
		project, target, state, created, completed := p.invitation(i)
		return []any{id, p.projects[project], p.users[p.teams[project][0]], p.users[target], state, created, completed}, err
	}); err != nil {
		return err
	}
	if err := copyRows("deliveries", []string{"id", "project_id", "direction", "status", "initiated_by_user_id", "completed_by_user_id", "created_at", "completed_at"}, p.profile.Deliveries, func(i int) ([]any, error) {
		id, err := uuid.NewV7()
		p.deliveries[i] = id
		m := p.movement(i)
		var actor *uuid.UUID
		if m.completed != nil {
			value := p.users[m.actor]
			actor = &value
		}
		return []any{id, p.projects[m.project], m.direction, m.status, p.users[m.sender], actor, m.created, m.completed}, err
	}); err != nil {
		return err
	}
	delivery, line := 0, 0
	if err := copyRows("delivery_items", []string{"delivery_id", "material_id", "quantity"}, p.profile.DeliveryItems, func(_ int) ([]any, error) {
		m := p.movement(delivery)
		material, amount := p.line(m, line)
		row := []any{p.deliveries[delivery], material, amount}
		line++
		if line == m.lines {
			line = 0
			delivery++
		}
		return row, nil
	}); err != nil {
		return err
	}
	activityCount := 0
	for project, team := range p.teams {
		m := p.movement((project+1)*(p.profile.Deliveries/p.profile.Projects) - 1)
		actor := m.sender
		if m.completed != nil {
			actor = m.actor
		}
		for _, user := range team {
			if user != actor {
				activityCount++
			}
		}
	}
	project, member = 0, 0
	return copyRows("notifications", []string{"id", "user_id", "project_id", "invitation_id", "type", "title", "message", "read_at", "created_at", "updated_at"}, p.profile.Invitations+activityCount, func(i int) ([]any, error) {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		if i < p.profile.Invitations {
			pr, target, _, created, completed := p.invitation(i)
			var read *time.Time
			if i%3 != 0 {
				at := created.Add(30 * time.Minute)
				if completed != nil {
					at = *completed
				}
				read = &at
			}
			return []any{id, p.users[target], p.projects[pr], p.invitations[i], "project.invitation", "Project invitation", "You have been invited to " + p.projects[pr] + ".", read, created, created}, nil
		}
		for {
			m := p.movement((project+1)*(p.profile.Deliveries/p.profile.Projects) - 1)
			actor := m.sender
			at := m.created
			kind := "sent"
			if m.completed != nil {
				actor = m.actor
				at = *m.completed
				kind = m.status
			}
			target := p.teams[project][member]
			member++
			pr := project
			if member == len(p.teams[project]) {
				project++
				member = 0
			}
			if target == actor {
				continue
			}
			return []any{id, p.users[target], p.projects[pr], nil, "delivery." + kind, "Delivery " + kind, "Delivery activity for " + p.projects[pr] + ".", nil, at, at}, nil
		}
	})
}
func date(at time.Time) pgtype.Date       { return pgtype.Date{Time: at, Valid: true} }
func parsedDate(value string) pgtype.Date { at, _ := time.Parse(time.DateOnly, value); return date(at) }

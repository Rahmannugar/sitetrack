package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	taskdb "sitetrack/internal/tasks/repositories/generated"
	"time"
)

func (s *Service) AddDependency(ctx context.Context, actor uuid.UUID, project, id, dependency string) (Dependency, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, true)
	if err != nil {
		return Dependency{}, err
	}
	defer rollback(tx)
	if _, err = s.read(ctx, q, project, id, actor); err != nil {
		return Dependency{}, err
	}
	target, err := s.read(ctx, q, project, dependency, actor)
	if err != nil {
		return Dependency{}, err
	}
	row, err := q.AddDependency(ctx, taskdb.AddDependencyParams{ProjectID: project, DependentTaskID: id, RequiredTaskID: dependency})
	if err != nil {
		return Dependency{}, failure(err)
	}
	if err = notify(ctx, tx, actor, project, id, "dependency_added"); err != nil {
		return Dependency{}, err
	}
	result := Dependency{RequiredTaskID: dependency, Name: target.Name, TaskStatus: target.Status, Status: string(row.Status)}
	return result, tx.Commit(ctx)
}
func (s *Service) RemoveDependency(ctx context.Context, actor uuid.UUID, project, id, dependency string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, true)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = s.read(ctx, q, project, id, actor); err != nil {
		return err
	}
	count, err := q.RemoveDependency(ctx, taskdb.RemoveDependencyParams{ProjectID: project, DependentTaskID: id, RequiredTaskID: dependency})
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	if err = notify(ctx, tx, actor, project, id, "dependency_removed"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type relationCursor struct {
	ID, Project, Task, Direction string
	Steps                        int32
}

func decodeRelation(value, project, id, direction string) (relationCursor, error) {
	var after relationCursor
	if value == "" {
		return after, nil
	}
	if len(value) > 1024 {
		return after, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(raw, &after) != nil || after.ID == "" || after.Project != project || after.Task != id || after.Direction != direction {
		return after, ErrInvalid
	}
	return after, nil
}
func (s *Service) Dependencies(ctx context.Context, actor uuid.UUID, project, id string, input DependencyListInput) (DependencyPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, false)
	if err != nil {
		return DependencyPage{}, err
	}
	defer rollback(tx)
	if _, err = s.read(ctx, q, project, id, actor); err != nil {
		return DependencyPage{}, err
	}
	if input.Limit < 1 || input.Limit > 100 {
		return DependencyPage{}, ErrInvalid
	}
	after, err := decodeRelation(input.Cursor, project, id, "direct")
	if err != nil {
		return DependencyPage{}, err
	}
	rows, err := q.ListDependencies(ctx, taskdb.ListDependenciesParams{ProjectID: project, DependentTaskID: id, AfterID: after.ID, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return DependencyPage{}, err
	}
	page := DependencyPage{Items: make([]Dependency, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, Dependency{RequiredTaskID: row.RequiredTaskID, Name: row.Name, Status: string(row.Status), TaskStatus: string(row.TaskStatus)})
	}
	if more {
		raw, err := json.Marshal(relationCursor{ID: page.Items[len(page.Items)-1].RequiredTaskID, Project: project, Task: id, Direction: "direct"})
		if err != nil {
			return DependencyPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, tx.Commit(ctx)
}
func (s *Service) Relationships(ctx context.Context, actor uuid.UUID, project, id string, input RelationshipInput) (RelationshipPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, false)
	if err != nil {
		return RelationshipPage{}, err
	}
	defer rollback(tx)
	if _, err = s.read(ctx, q, project, id, actor); err != nil {
		return RelationshipPage{}, err
	}
	if input.Limit < 1 || input.Limit > 100 || (input.Direction != "dependencies" && input.Direction != "dependents") {
		return RelationshipPage{}, ErrInvalid
	}
	after, err := decodeRelation(input.Cursor, project, id, input.Direction)
	if err != nil || after.Steps < 0 {
		return RelationshipPage{}, ErrInvalid
	}
	rows, err := q.ListRelationships(ctx, taskdb.ListRelationshipsParams{ProjectID: project, TaskID: id, Direction: input.Direction, AfterID: after.ID, AfterSteps: after.Steps, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return RelationshipPage{}, err
	}
	page := RelationshipPage{Items: make([]Relationship, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, Relationship{ID: row.ID, Name: row.Name, Status: string(row.Status), Steps: row.Steps})
	}
	if more {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(relationCursor{ID: last.ID, Steps: last.Steps, Project: project, Task: id, Direction: input.Direction})
		if err != nil {
			return RelationshipPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, tx.Commit(ctx)
}

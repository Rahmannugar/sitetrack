package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"sitetrack/internal/notifications"
	taskdb "sitetrack/internal/tasks/repositories/generated"
)

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool, now: time.Now} }
func (s *Service) today() pgtype.Date {
	at := s.now().UTC()
	return pgtype.Date{Time: time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}
func date(value string) (pgtype.Date, error) {
	if value == "" {
		return pgtype.Date{}, nil
	}
	at, err := time.Parse(time.DateOnly, value)
	if err != nil || at.Year() < 1 {
		return pgtype.Date{}, ErrInvalid
	}
	return pgtype.Date{Time: at, Valid: true}, nil
}
func validate(input Input) (Input, pgtype.Date, pgtype.Date, error) {
	input.Name = strings.TrimSpace(input.Name)
	start, e1 := date(input.StartDate)
	end, e2 := date(input.EndDate)
	size := utf8.RuneCountInString(input.Name)
	if size < 2 || size > 150 || e1 != nil || e2 != nil || !start.Valid || !end.Valid || end.Time.Before(start.Time) {
		return input, start, end, ErrInvalid
	}
	return input, start, end, nil
}
func failure(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var problem *pgconn.PgError
	if errors.As(err, &problem) {
		if problem.Code == "23505" {
			return fmt.Errorf("%w: dependency already exists", ErrConflict)
		}
		if problem.Code == "23514" {
			switch problem.ConstraintName {
			case "dependency_cycle":
				return fmt.Errorf("%w: dependency would create a cycle", ErrConflict)
			case "dependency_started":
				return fmt.Errorf("%w: started tasks require completed prerequisites", ErrConflict)
			case "task_blocked":
				return fmt.Errorf("%w: task has unfinished prerequisites", ErrConflict)
			case "task_reopen":
				return fmt.Errorf("%w: a dependent task has already started", ErrConflict)
			case "task_referenced":
				return fmt.Errorf("%w: remove dependent references before removing this task", ErrConflict)
			case "task_transition":
				return fmt.Errorf("%w: task action is not allowed", ErrConflict)
			}
		}
	}
	return err
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func (s *Service) locked(ctx context.Context, actor uuid.UUID, project string, write bool) (pgx.Tx, *taskdb.Queries, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	q := taskdb.New(tx)
	if write {
		_, err = q.LockProject(ctx, project)
	} else {
		_, err = q.ShareProject(ctx, project)
	}
	if err == nil {
		_, err = q.ProjectAccess(ctx, taskdb.ProjectAccessParams{ProjectID: project, Actor: actor})
	}
	if err != nil {
		rollback(tx)
		return nil, nil, failure(err)
	}
	return tx, q, nil
}
func task(row taskdb.GetTaskRow) Task {
	result := Task{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, StartDate: row.StartDate.Time.Format(time.DateOnly), EndDate: row.EndDate.Time.Format(time.DateOnly), Status: row.Status, Late: row.Late, Blocked: row.Blocked, DependsOn: row.DependsOn, DependencyCount: row.DependencyCount, DependentCount: row.DependentCount, HasMoreDependencies: row.DependencyCount > int64(len(row.DependsOn)), AllowedActions: []string{}, CreatedAt: row.CreatedAt.Time}
	if result.DependsOn == nil {
		result.DependsOn = []string{}
	}
	switch row.Status {
	case "not_started":
		if !row.Blocked {
			result.AllowedActions = append(result.AllowedActions, "start")
		}
	case "in_progress":
		result.AllowedActions = append(result.AllowedActions, "done")
	case "done":
		if row.CanReopen {
			result.AllowedActions = append(result.AllowedActions, "reopen")
		}
	}
	return result
}
func (s *Service) read(ctx context.Context, q *taskdb.Queries, project, id string) (Task, error) {
	row, err := q.GetTask(ctx, taskdb.GetTaskParams{ProjectID: project, ID: id, Today: s.today()})
	return task(row), failure(err)
}
func (s *Service) Get(ctx context.Context, actor uuid.UUID, project, id string) (Task, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, false)
	if err != nil {
		return Task{}, err
	}
	defer rollback(tx)
	result, err := s.read(ctx, q, project, id)
	if err != nil {
		return Task{}, err
	}
	return result, tx.Commit(ctx)
}
func (s *Service) Create(ctx context.Context, actor uuid.UUID, project string, input Input) (Task, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, true)
	if err != nil {
		return Task{}, err
	}
	defer rollback(tx)
	input, start, end, err := validate(input)
	if err != nil {
		return Task{}, err
	}
	number, err := q.NextTaskNumber(ctx, project)
	if err != nil {
		return Task{}, err
	}
	id := fmt.Sprintf("%s-T-%04d", project, number)
	if err = q.CreateTask(ctx, taskdb.CreateTaskParams{ID: id, ProjectID: project, Name: input.Name, StartDate: start, EndDate: end}); err != nil {
		return Task{}, failure(err)
	}
	if err = notify(ctx, tx, actor, project, id, "created"); err != nil {
		return Task{}, err
	}
	result, err := s.read(ctx, q, project, id)
	if err != nil {
		return Task{}, err
	}
	return result, tx.Commit(ctx)
}
func (s *Service) Update(ctx context.Context, actor uuid.UUID, project, id string, input Input) (Task, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, true)
	if err != nil {
		return Task{}, err
	}
	defer rollback(tx)
	if _, err = s.read(ctx, q, project, id); err != nil {
		return Task{}, err
	}
	input, start, end, err := validate(input)
	if err != nil {
		return Task{}, err
	}
	if err = q.UpdateTask(ctx, taskdb.UpdateTaskParams{ProjectID: project, ID: id, Name: input.Name, StartDate: start, EndDate: end}); err != nil {
		return Task{}, failure(err)
	}
	if err = notify(ctx, tx, actor, project, id, "updated"); err != nil {
		return Task{}, err
	}
	result, err := s.read(ctx, q, project, id)
	if err != nil {
		return Task{}, err
	}
	return result, tx.Commit(ctx)
}
func (s *Service) Remove(ctx context.Context, actor uuid.UUID, project, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, true)
	if err != nil {
		return err
	}
	defer rollback(tx)
	row, err := s.read(ctx, q, project, id)
	if err != nil {
		return err
	}
	if row.DependentCount > 0 {
		return fmt.Errorf("%w: remove dependent references before removing this task", ErrConflict)
	}
	if err = q.RemoveTaskDependencies(ctx, taskdb.RemoveTaskDependenciesParams{ProjectID: project, DependentTaskID: id}); err != nil {
		return err
	}
	if err = q.RemoveTask(ctx, taskdb.RemoveTaskParams{ProjectID: project, ID: id}); err != nil {
		return failure(err)
	}
	if err = notify(ctx, tx, actor, project, id, "removed"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) Transition(ctx context.Context, actor uuid.UUID, project, id, action string) (Task, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, true)
	if err != nil {
		return Task{}, err
	}
	defer rollback(tx)
	current, err := s.read(ctx, q, project, id)
	if err != nil {
		return Task{}, err
	}
	next := ""
	switch action {
	case "start":
		if current.Status == "not_started" {
			next = "in_progress"
		}
	case "done":
		if current.Status == "in_progress" {
			next = "done"
		}
	case "reopen":
		if current.Status == "done" {
			next = "in_progress"
		}
	default:
		return Task{}, ErrInvalid
	}
	if next == "" {
		return Task{}, fmt.Errorf("%w: task action is not allowed", ErrConflict)
	}
	if err = q.TransitionTask(ctx, taskdb.TransitionTaskParams{ProjectID: project, ID: id, Status: next}); err != nil {
		return Task{}, failure(err)
	}
	if err = notify(ctx, tx, actor, project, id, action); err != nil {
		return Task{}, err
	}
	result, err := s.read(ctx, q, project, id)
	if err != nil {
		return Task{}, err
	}
	return result, tx.Commit(ctx)
}
func notify(ctx context.Context, tx pgx.Tx, actor uuid.UUID, project, id, action string) error {
	return notifications.Create(ctx, tx, notifications.Input{ProjectID: project, ActorID: actor, Kind: "task." + action, Title: "Task activity", Message: id + ": " + action + "."}, nil)
}

package projects

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"sitetrack/internal/notifications"
	projectdb "sitetrack/internal/projects/repositories/generated"
)

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func validate(input Input) (Input, pgtype.Date, pgtype.Date, error) {
	input.Name = strings.TrimSpace(input.Name)
	start, e1 := time.Parse(time.DateOnly, input.StartDate)
	end, e2 := time.Parse(time.DateOnly, input.EndDate)
	size := utf8.RuneCountInString(input.Name)
	if size < 2 || size > 150 || e1 != nil || e2 != nil || end.Before(start) || start.Year() < 1 {
		return input, pgtype.Date{}, pgtype.Date{}, ErrInvalid
	}
	return input, pgtype.Date{Time: start, Valid: true}, pgtype.Date{Time: end, Valid: true}, nil
}

func (s *Service) Create(ctx context.Context, actor uuid.UUID, input Input) (Project, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	input, start, end, err := validate(input)
	if err != nil {
		return Project{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Project{}, err
	}
	defer rollback(tx)
	q := projectdb.New(tx)
	number, err := q.NextProjectNumber(ctx)
	if err != nil {
		return Project{}, err
	}
	id := fmt.Sprintf("PRJ-%04d", number)
	if _, err = q.CreateProject(ctx, projectdb.CreateProjectParams{ID: id, Name: input.Name, StartDate: start, EndDate: end}); err != nil {
		return Project{}, err
	}
	store, err := uuid.NewV7()
	if err != nil {
		return Project{}, err
	}
	if err = q.CreateProjectStore(ctx, projectdb.CreateProjectStoreParams{ID: store, ProjectID: id, Name: input.Name + " store"}); err != nil {
		return Project{}, err
	}
	if err = q.CreateMembership(ctx, projectdb.CreateMembershipParams{ProjectID: id, UserID: actor, IsAdmin: true}); err != nil {
		return Project{}, err
	}
	row, err := q.GetProject(ctx, projectdb.GetProjectParams{ID: id, Actor: actor})
	if err != nil {
		return Project{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Project{}, err
	}
	return project(row), nil
}

func project(row projectdb.GetProjectRow) Project {
	return Project{ID: row.ID, Name: row.Name, StartDate: row.StartDate.Time.Format(time.DateOnly), EndDate: row.EndDate.Time.Format(time.DateOnly), StoreID: row.StoreID, IsAdmin: row.IsAdmin, MemberCount: row.MemberCount, CreatedAt: row.CreatedAt.Time, TaskCount: row.TaskCount, NotStartedCount: row.NotStartedCount, InProgressCount: row.InProgressCount, DoneCount: row.DoneCount, LateCount: row.LateCount, BlockedCount: row.BlockedCount, CompletionPercentage: row.CompletionPercentage}
}
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func (s *Service) Get(ctx context.Context, actor uuid.UUID, id string) (Project, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	row, err := projectdb.New(s.pool).GetProject(ctx, projectdb.GetProjectParams{ID: id, Actor: actor})
	return project(row), notFound(err)
}

func (s *Service) locked(ctx context.Context, actor uuid.UUID, id string, write bool) (pgx.Tx, *projectdb.Queries, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	q := projectdb.New(tx)
	if write {
		_, err = q.LockProject(ctx, id)
	} else {
		_, err = q.ShareProject(ctx, id)
	}
	if err != nil {
		rollback(tx)
		return nil, nil, notFound(err)
	}
	admin, err := q.ProjectAccess(ctx, projectdb.ProjectAccessParams{ProjectID: id, Actor: actor})
	if err == nil && write && !admin {
		err = ErrForbidden
	}
	if err != nil {
		rollback(tx)
		return nil, nil, notFound(err)
	}
	return tx, q, nil
}

func (s *Service) Update(ctx context.Context, actor uuid.UUID, id string, input Input) (Project, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, id, true)
	if err != nil {
		return Project{}, err
	}
	defer rollback(tx)
	input, start, end, err := validate(input)
	if err != nil {
		return Project{}, err
	}
	if err = q.UpdateProject(ctx, projectdb.UpdateProjectParams{ID: id, Name: input.Name, StartDate: start, EndDate: end}); err != nil {
		return Project{}, err
	}
	row, err := q.GetProject(ctx, projectdb.GetProjectParams{ID: id, Actor: actor})
	if err != nil {
		return Project{}, err
	}
	if err = notifications.Create(ctx, tx, notifications.Input{ProjectID: id, ActorID: actor, Kind: "project.updated", Title: "Project updated", Message: "Project details were updated for " + id + "."}, nil); err != nil {
		return Project{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Project{}, err
	}
	return project(row), nil
}
func (s *Service) Remove(ctx context.Context, actor uuid.UUID, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, id, true)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = q.CancelProjectInvitations(ctx, id); err != nil {
		return err
	}
	if err = q.RemoveProject(ctx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type cursor struct {
	ID, Value, Sort, Search, Day string
	LateOnly                     bool
}

func (s *Service) List(ctx context.Context, actor uuid.UUID, input ListInput) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if input.Limit < 1 || input.Limit > 100 || utf8.RuneCountInString(input.Search) > 150 || len(input.Cursor) > 4096 {
		return Page{}, ErrInvalid
	}
	if input.Sort != "id" && input.Sort != "name" && input.Sort != "start_date" && input.Sort != "completion_percentage" && input.Sort != "late_count" {
		return Page{}, ErrInvalid
	}
	var after cursor
	var afterDate pgtype.Date
	var afterPercentage float64
	var afterCount int64
	day := time.Now().UTC().Format(time.DateOnly)
	if input.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after.ID == "" || after.Sort != input.Sort || after.Search != input.Search || after.LateOnly != input.LateOnly || after.Day != day {
			return Page{}, ErrInvalid
		}
	}
	if after.ID != "" {
		switch input.Sort {
		case "start_date":
			at, err := time.Parse(time.DateOnly, after.Value)
			if err != nil {
				return Page{}, ErrInvalid
			}
			afterDate = pgtype.Date{Time: at, Valid: true}
		case "completion_percentage":
			value, err := strconv.ParseFloat(after.Value, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
				return Page{}, ErrInvalid
			}
			afterPercentage = value
		case "late_count":
			value, err := strconv.ParseInt(after.Value, 10, 64)
			if err != nil || value < 0 {
				return Page{}, ErrInvalid
			}
			afterCount = value
		}
	}

	rows, err := projectdb.New(s.pool).ListProjects(ctx, projectdb.ListProjectsParams{Actor: actor, Search: input.Search, SortBy: input.Sort, AfterID: after.ID, AfterValue: after.Value, AfterDate: afterDate, AfterPercentage: afterPercentage, AfterCount: afterCount, LateOnly: input.LateOnly, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]Project, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, project(projectdb.GetProjectRow(row)))
	}
	if more {
		last := page.Items[len(page.Items)-1]
		value := last.ID
		if input.Sort == "name" {
			value = last.Name
		}
		if input.Sort == "start_date" {
			value = last.StartDate
		}
		if input.Sort == "completion_percentage" {
			value = strconv.FormatFloat(last.CompletionPercentage, 'f', 2, 64)
		}
		if input.Sort == "late_count" {
			value = strconv.FormatInt(last.LateCount, 10)
		}
		raw, err := json.Marshal(cursor{ID: last.ID, Value: value, Sort: input.Sort, Search: input.Search, LateOnly: input.LateOnly, Day: day})
		if err != nil {
			return Page{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

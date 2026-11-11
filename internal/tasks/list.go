package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	taskdb "sitetrack/internal/tasks/repositories/generated"
	"strconv"
	"time"
	"unicode/utf8"
)

type cursor struct{ ID, Value, Filter, Day string }

func (s *Service) List(ctx context.Context, actor uuid.UUID, project string, input ListInput) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, false)
	if err != nil {
		return Page{}, err
	}
	defer rollback(tx)
	if input.Limit < 1 || input.Limit > 100 || len(input.Cursor) > 4096 || utf8.RuneCountInString(input.Search) > 150 {
		return Page{}, ErrInvalid
	}
	switch input.Sort {
	case "id", "name", "start_date", "end_date", "dependent_count":
	default:
		return Page{}, ErrInvalid
	}
	if input.Order != "asc" && input.Order != "desc" {
		return Page{}, ErrInvalid
	}
	switch input.Status {
	case "", "not_started", "in_progress", "done":
	default:
		return Page{}, ErrInvalid
	}
	from, err := date(input.FromDate)
	if err != nil {
		return Page{}, err
	}
	to, err := date(input.ToDate)
	if err != nil || (from.Valid && to.Valid && from.Time.After(to.Time)) {
		return Page{}, ErrInvalid
	}
	filters := input
	filters.Cursor = ""
	filters.Limit = 0
	raw, err := json.Marshal(struct {
		Project string
		Input   ListInput
	}{project, filters})
	if err != nil {
		return Page{}, err
	}
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	today := s.today()
	var after cursor
	var afterDate pgtype.Date
	var afterCount int64
	if input.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after.ID == "" || after.Filter != fingerprint || after.Day != today.Time.Format(time.DateOnly) {
			return Page{}, ErrInvalid
		}
		if input.Sort == "start_date" || input.Sort == "end_date" {
			afterDate, err = date(after.Value)
			if err != nil || !afterDate.Valid {
				return Page{}, ErrInvalid
			}
		}
		if input.Sort == "dependent_count" {
			afterCount, err = strconv.ParseInt(after.Value, 10, 64)
			if err != nil || afterCount < 0 {
				return Page{}, ErrInvalid
			}
		}
	}
	rows, err := q.ListTasks(ctx, taskdb.ListTasksParams{ProjectID: project, Today: today, Status: input.Status, Search: input.Search, Late: input.Late, Blocked: input.Blocked, FromDate: from, ToDate: to, SortBy: input.Sort, SortOrder: input.Order, AfterID: after.ID, AfterValue: after.Value, AfterDate: afterDate, AfterCount: afterCount, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]Task, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, task(taskdb.GetTaskRow(row)))
	}
	if more {
		last := page.Items[len(page.Items)-1]
		value := last.ID
		switch input.Sort {
		case "name":
			value = last.Name
		case "start_date":
			value = last.StartDate
		case "end_date":
			value = last.EndDate
		case "dependent_count":
			value = strconv.FormatInt(last.DependentCount, 10)
		}
		raw, err := json.Marshal(cursor{ID: last.ID, Value: value, Filter: fingerprint, Day: today.Time.Format(time.DateOnly)})
		if err != nil {
			return Page{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, tx.Commit(ctx)
}

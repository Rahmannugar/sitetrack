package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	stockdb "sitetrack/internal/inventory/repositories/generated"
)

type cursor struct {
	ID          uuid.UUID
	Key, Filter string
	Date        *time.Time
}
type options struct{ Scope, Search, Category, Status, Sort, Order string }

func statusValid(value string) bool {
	return value == "out_of_stock" || value == "low_stock" || value == "available"
}
func searchPattern(value string) string {
	if value == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value) + "%"
}
func prepare(input options, value string, limit int, company bool) (options, cursor, uuid.NullUUID, error) {
	input.Search = strings.TrimSpace(input.Search)
	if limit < 1 || limit > 100 || len(value) > 4096 || utf8.RuneCountInString(input.Search) > 150 || (input.Order != "asc" && input.Order != "desc") {
		return options{}, cursor{}, uuid.NullUUID{}, ErrInvalid
	}
	if input.Sort != "name" && input.Sort != "quantity" && !(company && input.Sort == "status") && !(!company && input.Sort == "last_delivery") {
		return options{}, cursor{}, uuid.NullUUID{}, ErrInvalid
	}
	if input.Status != "" && (!company || !statusValid(input.Status)) {
		return options{}, cursor{}, uuid.NullUUID{}, ErrInvalid
	}
	var category uuid.NullUUID
	if input.Category != "" {
		id, err := uuid.Parse(input.Category)
		if err != nil || id == uuid.Nil {
			return options{}, cursor{}, uuid.NullUUID{}, ErrInvalid
		}
		category = uuid.NullUUID{UUID: id, Valid: true}
		input.Category = id.String()
	}
	raw, _ := json.Marshal(input)
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	after := cursor{Filter: fingerprint}
	if value != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || json.Unmarshal(decoded, &after) != nil || after.ID == uuid.Nil || after.Filter != fingerprint {
			return options{}, cursor{}, uuid.NullUUID{}, ErrInvalid
		}
		switch input.Sort {
		case "name":
			if after.Key == "" {
				return options{}, cursor{}, uuid.NullUUID{}, ErrInvalid
			}
		case "quantity":
			if _, err := quantity(after.Key, false); err != nil {
				return options{}, cursor{}, uuid.NullUUID{}, ErrInvalid
			}
		case "status":
			if !statusValid(after.Key) {
				return options{}, cursor{}, uuid.NullUUID{}, ErrInvalid
			}
		}
	}
	return input, after, category, nil
}
func encodeCursor(after cursor) (string, error) {
	raw, err := json.Marshal(after)
	return base64.RawURLEncoding.EncodeToString(raw), err
}

func (s *Service) CompanyStock(ctx context.Context, actor uuid.UUID, input CompanyListInput) (result CompanyPage, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "inventory.company.list", actor)
	defer func() { finish(resultErr, uuid.Nil) }()
	if err := s.companyAccess(ctx, actor); err != nil {
		return CompanyPage{}, err
	}
	q := stockdb.New(s.pool)
	opts, after, category, err := prepare(options{Scope: "company", Search: input.Search, Category: input.CategoryID, Status: input.Status, Sort: input.Sort, Order: input.Order}, input.Cursor, input.Limit, true)
	if err != nil {
		return CompanyPage{}, err
	}
	afterQuantity, afterStatus := "0.000", "out_of_stock"
	if opts.Sort == "quantity" && after.ID != uuid.Nil {
		afterQuantity = after.Key
	}
	if opts.Sort == "status" && after.ID != uuid.Nil {
		afterStatus = after.Key
	}
	rows, err := q.ListCompanyStock(ctx, stockdb.ListCompanyStockParams{Search: searchPattern(opts.Search), CategoryID: category, Status: opts.Status, AfterID: uuid.NullUUID{UUID: after.ID, Valid: after.ID != uuid.Nil}, SortBy: opts.Sort, SortOrder: opts.Order, AfterName: after.Key, AfterQuantity: afterQuantity, AfterStatus: afterStatus, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return CompanyPage{}, err
	}
	page := CompanyPage{Items: make([]CompanyItem, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, companyItem(stockdb.CompanyItemRow{MaterialID: row.MaterialID, Name: row.Name, CategoryID: row.CategoryID, CategoryName: row.CategoryName, Unit: row.Unit, Quantity: row.Quantity, ReorderPoint: row.ReorderPoint, InTransitToProjects: row.InTransitToProjects, Status: row.Status, UpdatedAt: row.UpdatedAt}))
	}
	if more {
		last := rows[len(rows)-1]
		key := last.SortName
		if opts.Sort == "quantity" {
			key = last.Quantity
		}
		if opts.Sort == "status" {
			key = string(last.Status)
		}
		page.NextCursor, err = encodeCursor(cursor{ID: last.MaterialID, Key: key, Filter: after.Filter})
	}
	return page, err
}
func (s *Service) ProjectStock(ctx context.Context, actor uuid.UUID, project string, input ProjectListInput) (result ProjectPage, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "inventory.project.list", actor)
	defer func() { finish(resultErr, uuid.Nil) }()
	tx, q, err := s.project(ctx, actor, project)
	if err != nil {
		return ProjectPage{}, err
	}
	defer rollback(tx)
	opts, after, category, err := prepare(options{Scope: project, Search: input.Search, Category: input.CategoryID, Sort: input.Sort, Order: input.Order}, input.Cursor, input.Limit, false)
	if err != nil {
		return ProjectPage{}, err
	}
	afterQuantity := "0.000"
	if opts.Sort == "quantity" && after.ID != uuid.Nil {
		afterQuantity = after.Key
	}
	var afterDate pgtype.Timestamptz
	if opts.Sort == "last_delivery" && after.Date != nil {
		afterDate = pgtype.Timestamptz{Time: *after.Date, Valid: true}
	}
	rows, err := q.ListProjectStock(ctx, stockdb.ListProjectStockParams{ProjectID: project, Search: searchPattern(opts.Search), CategoryID: category, AfterID: uuid.NullUUID{UUID: after.ID, Valid: after.ID != uuid.Nil}, SortBy: opts.Sort, SortOrder: opts.Order, AfterName: after.Key, AfterQuantity: afterQuantity, AfterDelivery: afterDate, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return ProjectPage{}, err
	}
	page := ProjectPage{Items: make([]ProjectItem, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, projectItem(stockdb.ProjectItemRow{MaterialID: row.MaterialID, Name: row.Name, CategoryID: row.CategoryID, CategoryName: row.CategoryName, Unit: row.Unit, Quantity: row.Quantity, LastDeliveryAt: row.LastDeliveryAt, UpdatedAt: row.UpdatedAt}))
	}
	if more {
		last := rows[len(rows)-1]
		key := last.SortName
		if opts.Sort == "quantity" {
			key = last.Quantity
		}
		var date *time.Time
		if last.LastDeliveryAt.Valid {
			date = &last.LastDeliveryAt.Time
		}
		page.NextCursor, err = encodeCursor(cursor{ID: last.MaterialID, Key: key, Filter: after.Filter, Date: date})
		if err != nil {
			return ProjectPage{}, err
		}
	}
	return page, tx.Commit(ctx)
}

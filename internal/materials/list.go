package materials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	cataloguedb "sitetrack/internal/materials/repositories/generated"
)

type cursor struct {
	ID                           uuid.UUID
	Name, Kind, Search, Category string
}

func pageCursor(value, kind, search, category string) (cursor, error) {
	if value == "" {
		return cursor{}, nil
	}
	if len(value) > 4096 {
		return cursor{}, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var after cursor
	if err != nil || json.Unmarshal(raw, &after) != nil || after.ID == uuid.Nil || after.Name == "" || after.Kind != kind || after.Search != search || after.Category != category {
		return cursor{}, ErrInvalid
	}
	return after, nil
}
func encodeCursor(id uuid.UUID, value, kind, search, category string) (string, error) {
	raw, err := json.Marshal(cursor{ID: id, Name: value, Kind: kind, Search: search, Category: category})
	return base64.RawURLEncoding.EncodeToString(raw), err
}
func searchPattern(value string) string {
	if value == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value) + "%"
}
func (s *Service) Categories(ctx context.Context, actor uuid.UUID, input CategoryListInput) (result CategoryPage, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "material_categories.list", actor)
	defer func() { finish(resultErr, uuid.Nil) }()
	if err := s.authorize(ctx, actor, false); err != nil {
		return CategoryPage{}, err
	}
	input.Search = strings.TrimSpace(input.Search)
	if input.Limit < 1 || input.Limit > 100 || utf8.RuneCountInString(input.Search) > 100 {
		return CategoryPage{}, ErrInvalid
	}
	after, err := pageCursor(input.Cursor, "categories", input.Search, "")
	if err != nil {
		return CategoryPage{}, err
	}
	rows, err := cataloguedb.New(s.pool).ListCategories(ctx, cataloguedb.ListCategoriesParams{Search: searchPattern(input.Search), AfterID: uuid.NullUUID{UUID: after.ID, Valid: after.ID != uuid.Nil}, AfterName: after.Name, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return CategoryPage{}, err
	}
	page := CategoryPage{Items: make([]Category, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, category(cataloguedb.SitetrackMaterialCategory{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}))
	}
	if more {
		last := rows[len(rows)-1]
		page.NextCursor, err = encodeCursor(last.ID, last.SortName, "categories", input.Search, "")
	}
	return page, err
}
func (s *Service) List(ctx context.Context, actor uuid.UUID, input ListInput) (result Page, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "materials.list", actor)
	defer func() { finish(resultErr, uuid.Nil) }()
	if err := s.authorize(ctx, actor, false); err != nil {
		return Page{}, err
	}
	input.Search = strings.TrimSpace(input.Search)
	if input.Limit < 1 || input.Limit > 100 || utf8.RuneCountInString(input.Search) > 150 {
		return Page{}, ErrInvalid
	}
	var categoryID uuid.NullUUID
	if input.CategoryID != "" {
		value, err := uuid.Parse(input.CategoryID)
		if err != nil || value == uuid.Nil {
			return Page{}, ErrInvalid
		}
		categoryID = uuid.NullUUID{UUID: value, Valid: true}
		input.CategoryID = value.String()
	}
	after, err := pageCursor(input.Cursor, "materials", input.Search, input.CategoryID)
	if err != nil {
		return Page{}, err
	}
	rows, err := cataloguedb.New(s.pool).ListMaterials(ctx, cataloguedb.ListMaterialsParams{Search: searchPattern(input.Search), CategoryID: categoryID, AfterID: uuid.NullUUID{UUID: after.ID, Valid: after.ID != uuid.Nil}, AfterName: after.Name, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]Material, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, material(cataloguedb.GetMaterialRow(row)))
	}
	if more {
		last := rows[len(rows)-1]
		page.NextCursor, err = encodeCursor(last.ID, last.SortName, "materials", input.Search, input.CategoryID)
	}
	return page, err
}

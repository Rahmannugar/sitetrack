package deliveries

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	deliverydb "sitetrack/internal/deliveries/repositories/generated"
)

func delivery(row deliverydb.GetDeliveryRow) Delivery {
	result := Delivery{ID: row.ID, ProjectID: row.ProjectID, ProjectName: row.ProjectName, Direction: string(row.Direction), Status: string(row.Status), InitiatedByUserID: row.InitiatedByUserID, InitiatedByName: row.InitiatedByName, CreatedAt: row.CreatedAt.Time, CompletedByName: row.CompletedByName, Items: []Item{}}
	if row.CompletedByUserID.Valid {
		result.CompletedByUserID = &row.CompletedByUserID.UUID
	}
	if row.CompletedAt.Valid {
		result.CompletedAt = &row.CompletedAt.Time
	}
	return result
}
func attachItems(ctx context.Context, q *deliverydb.Queries, deliveries []Delivery) error {
	if len(deliveries) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(deliveries))
	positions := make(map[uuid.UUID]int, len(deliveries))
	for i, d := range deliveries {
		ids[i] = d.ID
		positions[d.ID] = i
	}
	rows, err := q.DeliveryItems(ctx, ids)
	if err != nil {
		return err
	}
	for _, row := range rows {
		index := positions[row.DeliveryID]
		deliveries[index].Items = append(deliveries[index].Items, Item{MaterialID: row.MaterialID, Name: row.Name, Unit: row.Unit, Quantity: row.Quantity})
	}
	return nil
}
func read(ctx context.Context, q *deliverydb.Queries, id uuid.UUID) (Delivery, error) {
	row, err := q.GetDelivery(ctx, id)
	if err != nil {
		return Delivery{}, failure(err)
	}
	result := []Delivery{delivery(row)}
	if err := attachItems(ctx, q, result); err != nil {
		return Delivery{}, err
	}
	return result[0], nil
}
func (s *Service) Get(ctx context.Context, actor, id uuid.UUID) (result Delivery, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "deliveries.get", actor)
	defer func() { finish(resultErr, id) }()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, err
	}
	defer rollback(tx)
	q := deliverydb.New(tx)
	project, err := q.DeliveryProject(ctx, id)
	if err != nil {
		return Delivery{}, failure(err)
	}
	if _, err := q.ShareProject(ctx, project); err != nil {
		return Delivery{}, failure(err)
	}
	access, err := q.ProjectAccess(ctx, deliverydb.ProjectAccessParams{Actor: actor, ProjectID: project})
	if err != nil {
		return Delivery{}, failure(err)
	}
	if !access.Company && !access.Readable {
		return Delivery{}, ErrNotFound
	}
	result, err = read(ctx, q, id)
	if err != nil {
		return Delivery{}, err
	}
	return result, tx.Commit(ctx)
}

type cursor struct {
	ID     uuid.UUID
	Time   time.Time
	Filter string
}

func listParams(input ListInput, scope string) (deliverydb.ListProjectDeliveriesParams, string, error) {
	params := deliverydb.ListProjectDeliveriesParams{ProjectID: input.ProjectID, Direction: input.Direction, Status: input.Status, PageSize: int32(input.Limit + 1)}
	if input.Limit < 1 || input.Limit > 100 || len(input.Cursor) > 4096 || len(input.ProjectID) > 32 {
		return params, "", ErrInvalid
	}
	if input.Direction != "" && input.Direction != "company_to_project" && input.Direction != "project_to_company" {
		return params, "", ErrInvalid
	}
	if input.Status != "" && input.Status != "in_transit" && input.Status != "delivered" && input.Status != "rejected" && input.Status != "cancelled" {
		return params, "", ErrInvalid
	}
	if input.From != "" {
		at, err := time.Parse(time.DateOnly, input.From)
		if err != nil || at.Year() < 1 {
			return params, "", ErrInvalid
		}
		params.FromDate = pgtype.Timestamptz{Time: at, Valid: true}
	}
	if input.To != "" {
		at, err := time.Parse(time.DateOnly, input.To)
		if err != nil || at.Year() < 1 {
			return params, "", ErrInvalid
		}
		params.ToDate = pgtype.Timestamptz{Time: at.AddDate(0, 0, 1), Valid: true}
	}
	if params.FromDate.Valid && params.ToDate.Valid && !params.FromDate.Time.Before(params.ToDate.Time) {
		return params, "", ErrInvalid
	}
	raw, _ := json.Marshal(struct{ Scope, Project, Direction, Status, From, To string }{scope, input.ProjectID, input.Direction, input.Status, input.From, input.To})
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	if input.Cursor != "" {
		var after cursor
		decoded, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || json.Unmarshal(decoded, &after) != nil || after.ID == uuid.Nil || after.Time.IsZero() || after.Filter != fingerprint {
			return params, "", ErrInvalid
		}
		params.AfterID = uuid.NullUUID{UUID: after.ID, Valid: true}
		params.AfterTime = pgtype.Timestamptz{Time: after.Time, Valid: true}
	}
	return params, fingerprint, nil
}
func (s *Service) List(ctx context.Context, actor uuid.UUID, project string, input ListInput) (result Page, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "deliveries.list", actor)
	defer func() { finish(resultErr, uuid.Nil) }()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Page{}, err
	}
	defer rollback(tx)
	q := deliverydb.New(tx)
	scope := "company"
	if project == "" {
		allowed, err := q.AccountRole(ctx, actor)
		if err != nil {
			return Page{}, failure(err)
		}
		if !allowed {
			return Page{}, ErrForbidden
		}
	} else {
		if _, err := q.ShareProject(ctx, project); err != nil {
			return Page{}, failure(err)
		}
		access, err := q.ProjectAccess(ctx, deliverydb.ProjectAccessParams{Actor: actor, ProjectID: project})
		if err != nil {
			return Page{}, failure(err)
		}
		if !access.Readable {
			return Page{}, ErrNotFound
		}
		if input.ProjectID != "" && input.ProjectID != project {
			return Page{}, ErrInvalid
		}
		input.ProjectID = project
		scope = project
	}
	params, filter, err := listParams(input, scope)
	if err != nil {
		return Page{}, err
	}
	var rows []deliverydb.ListProjectDeliveriesRow
	if params.ProjectID != "" {
		rows, err = q.ListProjectDeliveries(ctx, params)
	} else {
		companyRows, readErr := q.ListCompanyDeliveries(ctx, deliverydb.ListCompanyDeliveriesParams{
			Direction: params.Direction, Status: params.Status, FromDate: params.FromDate, ToDate: params.ToDate,
			AfterID: params.AfterID, AfterTime: params.AfterTime, PageSize: params.PageSize,
		})
		err = readErr
		rows = make([]deliverydb.ListProjectDeliveriesRow, len(companyRows))
		for i, row := range companyRows {
			rows[i] = deliverydb.ListProjectDeliveriesRow(row)
		}
	}
	if err != nil {
		return Page{}, err
	}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	result = Page{Items: make([]Delivery, len(rows))}
	for i, row := range rows {
		result.Items[i] = delivery(deliverydb.GetDeliveryRow(row))
	}
	if err := attachItems(ctx, q, result.Items); err != nil {
		return Page{}, err
	}
	if more {
		last := result.Items[len(result.Items)-1]
		raw, _ := json.Marshal(cursor{ID: last.ID, Time: last.CreatedAt, Filter: filter})
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, tx.Commit(ctx)
}

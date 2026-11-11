package inventory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"sitetrack/internal/infra/telemetry"
	stockdb "sitetrack/internal/inventory/repositories/generated"
)

type Service struct {
	pool       *pgxpool.Pool
	monitoring *telemetry.Runtime
	operations metric.Int64Counter
	duration   metric.Float64Histogram
}

func NewService(pool *pgxpool.Pool, monitoring *telemetry.Runtime) (*Service, error) {
	operations, err := monitoring.Meter.Int64Counter("inventory.operations", metric.WithUnit("{operation}"))
	if err != nil {
		return nil, err
	}
	duration, err := monitoring.Meter.Float64Histogram("inventory.operation.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5))
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, monitoring: monitoring, operations: operations, duration: duration}, nil
}
func failure(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var problem *pgconn.PgError
	if errors.As(err, &problem) && problem.Code == "22003" {
		return ErrConflict
	}
	return err
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func quantity(value string, positive bool) (string, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) < 1 || len(parts[0]) > 15 || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return "", ErrInvalid
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) < 1 || len(fraction) > 3 {
			return "", ErrInvalid
		}
	}
	for _, digit := range parts[0] + fraction {
		if digit < '0' || digit > '9' {
			return "", ErrInvalid
		}
	}
	fraction += strings.Repeat("0", 3-len(fraction))
	milli, err := strconv.ParseInt(parts[0]+fraction, 10, 64)
	if err != nil || (positive && milli == 0) {
		return "", ErrInvalid
	}
	return fmt.Sprintf("%d.%03d", milli/1000, milli%1000), nil
}
func (s *Service) EnsureCompanyStore(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return stockdb.New(s.pool).EnsureCompanyStore(ctx, id)
}
func (s *Service) companyAccess(ctx context.Context, actor uuid.UUID) error {
	allowed, err := stockdb.New(s.pool).CompanyAccess(ctx, actor)
	if err != nil {
		return failure(err)
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}
func (s *Service) CompanyStore(ctx context.Context, actor uuid.UUID) (result Store, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "inventory.company.get", actor)
	defer func() { finish(resultErr, uuid.Nil) }()
	if err := s.companyAccess(ctx, actor); err != nil {
		return Store{}, err
	}
	row, err := stockdb.New(s.pool).CompanyStore(ctx)
	return Store{ID: row.ID, Name: row.Name, CanManage: true}, failure(err)
}
func (s *Service) project(ctx context.Context, actor uuid.UUID, project string) (pgx.Tx, *stockdb.Queries, Store, error) {
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("project.id", project))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, Store{}, err
	}
	q := stockdb.New(tx)
	if _, err = q.LockProject(ctx, project); err != nil {
		rollback(tx)
		return nil, nil, Store{}, failure(err)
	}
	row, err := q.ProjectStore(ctx, stockdb.ProjectStoreParams{Actor: actor, ProjectID: project})
	if err != nil {
		rollback(tx)
		return nil, nil, Store{}, failure(err)
	}
	return tx, q, Store{ID: row.ID, Name: row.Name, ProjectID: row.ProjectID, CanManage: row.IsAdmin}, nil
}
func (s *Service) ProjectStore(ctx context.Context, actor uuid.UUID, project string) (result Store, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "inventory.project.get", actor)
	defer func() { finish(resultErr, uuid.Nil) }()
	tx, _, result, err := s.project(ctx, actor, project)
	if err != nil {
		return Store{}, err
	}
	defer rollback(tx)
	return result, tx.Commit(ctx)
}
func companyItem(row stockdb.CompanyItemRow) CompanyItem {
	return CompanyItem{StoreID: row.StoreID, MaterialID: row.MaterialID, Name: row.Name, CategoryID: row.CategoryID, CategoryName: row.CategoryName, Unit: row.Unit, Quantity: row.Quantity, ReorderPoint: row.ReorderPoint, InTransitToProjects: row.InTransitToProjects, Status: string(row.Status), UpdatedAt: row.UpdatedAt.Time}
}
func projectItem(row stockdb.ProjectItemRow) ProjectItem {
	result := ProjectItem{StoreID: row.StoreID, MaterialID: row.MaterialID, Name: row.Name, CategoryID: row.CategoryID, CategoryName: row.CategoryName, Unit: row.Unit, Quantity: row.Quantity, UpdatedAt: row.UpdatedAt.Time}
	if row.LastDeliveryAt.Valid {
		result.LastDeliveryAt = &row.LastDeliveryAt.Time
	}
	return result
}
func (s *Service) CompanyItem(ctx context.Context, actor, id uuid.UUID) (result CompanyItem, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "inventory.company.item", actor)
	defer func() { finish(resultErr, id) }()
	if err := s.companyAccess(ctx, actor); err != nil {
		return CompanyItem{}, err
	}
	q := stockdb.New(s.pool)
	store, err := q.CompanyStore(ctx)
	if err != nil {
		return CompanyItem{}, failure(err)
	}
	row, err := q.CompanyItem(ctx, stockdb.CompanyItemParams{StoreID: store.ID, MaterialID: id})
	return companyItem(row), failure(err)
}
func (s *Service) ProjectItem(ctx context.Context, actor uuid.UUID, project string, id uuid.UUID) (result ProjectItem, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "inventory.project.item", actor)
	defer func() { finish(resultErr, id) }()
	tx, q, _, err := s.project(ctx, actor, project)
	if err != nil {
		return ProjectItem{}, err
	}
	defer rollback(tx)
	row, err := q.ProjectItem(ctx, stockdb.ProjectItemParams{ProjectID: project, MaterialID: id})
	if err != nil {
		return ProjectItem{}, failure(err)
	}
	return projectItem(row), tx.Commit(ctx)
}
func (s *Service) AddStock(ctx context.Context, actor, id uuid.UUID, input QuantityInput) (CompanyItem, error) {
	return s.change(ctx, actor, id, input.Quantity, true)
}
func (s *Service) SetReorderPoint(ctx context.Context, actor, id uuid.UUID, input ReorderInput) (CompanyItem, error) {
	return s.change(ctx, actor, id, input.ReorderPoint, false)
}
func (s *Service) change(ctx context.Context, actor, id uuid.UUID, raw string, add bool) (result CompanyItem, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	operation := "inventory.company.reorder_point"
	if add {
		operation = "inventory.company.add_stock"
	}
	ctx, finish := s.start(ctx, operation, actor)
	defer func() { finish(resultErr, id) }()
	if err := s.companyAccess(ctx, actor); err != nil {
		return CompanyItem{}, err
	}
	amount, err := quantity(raw, add)
	if err != nil {
		return CompanyItem{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CompanyItem{}, err
	}
	defer rollback(tx)
	q := stockdb.New(tx)
	store, err := q.CompanyStore(ctx)
	if err != nil {
		return CompanyItem{}, failure(err)
	}
	// We lock the material before stock changes to serialize them with unit edits and removal.
	if _, err := q.LockMaterial(ctx, id); err != nil {
		return CompanyItem{}, failure(err)
	}
	if add {
		err = q.AddStock(ctx, stockdb.AddStockParams{StoreID: store.ID, MaterialID: id, Quantity: amount})
	} else {
		err = q.SetReorderPoint(ctx, stockdb.SetReorderPointParams{StoreID: store.ID, MaterialID: id, ReorderPoint: amount})
	}
	if err != nil {
		return CompanyItem{}, failure(err)
	}
	row, err := q.CompanyItem(ctx, stockdb.CompanyItemParams{StoreID: store.ID, MaterialID: id})
	if err != nil {
		return CompanyItem{}, err
	}
	return companyItem(row), tx.Commit(ctx)
}

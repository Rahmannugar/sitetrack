package materials

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
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"

	"sitetrack/internal/infra/telemetry"
	cataloguedb "sitetrack/internal/materials/repositories/generated"
)

type Service struct {
	pool       *pgxpool.Pool
	monitoring *telemetry.Runtime
	operations metric.Int64Counter
	duration   metric.Float64Histogram
}

func NewService(pool *pgxpool.Pool, monitoring *telemetry.Runtime) (*Service, error) {
	operations, err := monitoring.Meter.Int64Counter("catalogue.operations", metric.WithUnit("{operation}"))
	if err != nil {
		return nil, err
	}
	duration, err := monitoring.Meter.Float64Histogram("catalogue.operation.duration", metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5))
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
	if errors.As(err, &problem) && problem.Code == "23505" {
		return fmt.Errorf("%w: name already in use", ErrConflict)
	}
	return err
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func (s *Service) authorize(ctx context.Context, actor uuid.UUID, write bool) error {
	canManage, err := cataloguedb.New(s.pool).AccountAccess(ctx, actor)
	if err != nil {
		return failure(err)
	}
	if write && !canManage {
		return ErrForbidden
	}
	return nil
}
func category(row cataloguedb.SitetrackMaterialCategory) Category {
	return Category{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}
func material(row cataloguedb.GetMaterialRow) Material {
	return Material{ID: row.ID, Name: row.Name, CategoryID: row.CategoryID, CategoryName: row.CategoryName, Unit: row.Unit, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}
func name(value string, maximum int) (string, error) {
	value = strings.TrimSpace(value)
	if count := utf8.RuneCountInString(value); count < 2 || count > maximum {
		return "", ErrInvalid
	}
	return value, nil
}
func validate(input Input) (Input, uuid.UUID, error) {
	var err error
	input.Name, err = name(input.Name, 150)
	if err != nil {
		return Input{}, uuid.Nil, err
	}
	input.Unit = strings.TrimSpace(input.Unit)
	id, idErr := uuid.Parse(input.CategoryID)
	if count := utf8.RuneCountInString(input.Unit); count < 1 || count > 24 || idErr != nil || id == uuid.Nil {
		return Input{}, uuid.Nil, ErrInvalid
	}
	return input, id, nil
}

func (s *Service) CreateCategory(ctx context.Context, actor uuid.UUID, input CategoryInput) (result Category, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "material_categories.create", actor)
	defer func() { finish(resultErr, result.ID) }()
	if err := s.authorize(ctx, actor, true); err != nil {
		return Category{}, err
	}
	value, err := name(input.Name, 100)
	if err != nil {
		return Category{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Category{}, err
	}
	row, err := cataloguedb.New(s.pool).CreateCategory(ctx, cataloguedb.CreateCategoryParams{ID: id, Name: value})
	return category(row), failure(err)
}
func (s *Service) GetCategory(ctx context.Context, actor, id uuid.UUID) (result Category, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "material_categories.get", actor)
	defer func() { finish(resultErr, id) }()
	if err := s.authorize(ctx, actor, false); err != nil {
		return Category{}, err
	}
	row, err := cataloguedb.New(s.pool).GetCategory(ctx, id)
	return category(row), failure(err)
}
func (s *Service) UpdateCategory(ctx context.Context, actor, id uuid.UUID, input CategoryInput) (result Category, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "material_categories.update", actor)
	defer func() { finish(resultErr, id) }()
	if err := s.authorize(ctx, actor, true); err != nil {
		return Category{}, err
	}
	value, err := name(input.Name, 100)
	if err != nil {
		return Category{}, err
	}
	row, err := cataloguedb.New(s.pool).UpdateCategory(ctx, cataloguedb.UpdateCategoryParams{ID: id, Name: value})
	return category(row), failure(err)
}
func (s *Service) RemoveCategory(ctx context.Context, actor, id uuid.UUID) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "material_categories.remove", actor)
	defer func() { finish(resultErr, id) }()
	if err := s.authorize(ctx, actor, true); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	q := cataloguedb.New(tx)
	if _, err := q.LockCategory(ctx, id); err != nil {
		return failure(err)
	}
	used, err := q.CategoryInUse(ctx, id)
	if err != nil {
		return err
	}
	if used {
		return fmt.Errorf("%w: category still has materials", ErrConflict)
	}
	if _, err := q.RemoveCategory(ctx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) Create(ctx context.Context, actor uuid.UUID, input Input) (result Material, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "materials.create", actor)
	defer func() { finish(resultErr, result.ID) }()
	if err := s.authorize(ctx, actor, true); err != nil {
		return Material{}, err
	}
	input, categoryID, err := validate(input)
	if err != nil {
		return Material{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Material{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Material{}, err
	}
	defer rollback(tx)
	q := cataloguedb.New(tx)
	// We share the category lock with removal so new materials cannot use a removed category.
	if _, err := q.LockCategory(ctx, categoryID); err != nil {
		return Material{}, failure(err)
	}
	if _, err := q.CreateMaterial(ctx, cataloguedb.CreateMaterialParams{ID: id, Name: input.Name, CategoryID: categoryID, Unit: input.Unit}); err != nil {
		return Material{}, failure(err)
	}
	row, err := q.GetMaterial(ctx, id)
	if err != nil {
		return Material{}, err
	}
	return material(row), tx.Commit(ctx)
}
func (s *Service) Get(ctx context.Context, actor, id uuid.UUID) (result Material, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "materials.get", actor)
	defer func() { finish(resultErr, id) }()
	if err := s.authorize(ctx, actor, false); err != nil {
		return Material{}, err
	}
	row, err := cataloguedb.New(s.pool).GetMaterial(ctx, id)
	return material(row), failure(err)
}
func (s *Service) Update(ctx context.Context, actor, id uuid.UUID, input Input) (result Material, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "materials.update", actor)
	defer func() { finish(resultErr, id) }()
	if err := s.authorize(ctx, actor, true); err != nil {
		return Material{}, err
	}
	input, categoryID, err := validate(input)
	if err != nil {
		return Material{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Material{}, err
	}
	defer rollback(tx)
	q := cataloguedb.New(tx)
	if _, err := q.LockMaterial(ctx, id); err != nil {
		return Material{}, failure(err)
	}
	if _, err := q.LockCategory(ctx, categoryID); err != nil {
		return Material{}, failure(err)
	}
	if err := q.UpdateMaterial(ctx, cataloguedb.UpdateMaterialParams{ID: id, Name: input.Name, CategoryID: categoryID, Unit: input.Unit}); err != nil {
		return Material{}, failure(err)
	}
	row, err := q.GetMaterial(ctx, id)
	if err != nil {
		return Material{}, err
	}
	return material(row), tx.Commit(ctx)
}
func (s *Service) Remove(ctx context.Context, actor, id uuid.UUID) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "materials.remove", actor)
	defer func() { finish(resultErr, id) }()
	if err := s.authorize(ctx, actor, true); err != nil {
		return err
	}
	count, err := cataloguedb.New(s.pool).RemoveMaterial(ctx, id)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

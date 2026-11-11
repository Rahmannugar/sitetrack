package deliveries

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"

	deliverydb "sitetrack/internal/deliveries/repositories/generated"
	"sitetrack/internal/infra/quantity"
	"sitetrack/internal/infra/telemetry"
	"sitetrack/internal/notifications"
)

type Service struct {
	pool       *pgxpool.Pool
	monitoring *telemetry.Runtime
	operations metric.Int64Counter
	duration   metric.Float64Histogram
}

func NewService(pool *pgxpool.Pool, monitoring *telemetry.Runtime) (*Service, error) {
	operations, err := monitoring.Meter.Int64Counter("deliveries.operations", metric.WithUnit("{operation}"))
	if err != nil {
		return nil, err
	}
	duration, err := monitoring.Meter.Float64Histogram("deliveries.operation.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5))
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
	if errors.As(err, &problem) && (problem.Code == "22003" || problem.ConstraintName == "company_inventory_capacity" || problem.ConstraintName == "project_inventory_capacity") {
		return ErrCapacity
	}
	return err
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func authorize(access deliverydb.ProjectAccessRow, company bool) error {
	if !access.Company && !access.Readable {
		return ErrNotFound
	}
	if access.Removed {
		return ErrNotFound
	}
	if company && !access.Company {
		return ErrForbidden
	}
	if !company && !access.Readable {
		return ErrNotFound
	}
	if !company && !access.Admin {
		return ErrForbidden
	}
	return nil
}
func validate(input Input) ([]uuid.UUID, []string, error) {
	if input.ProjectID == "" || len(input.ProjectID) > 32 || (input.Direction != "company_to_project" && input.Direction != "project_to_company") || len(input.Items) < 1 || len(input.Items) > 100 {
		return nil, nil, ErrInvalid
	}
	type line struct {
		id     uuid.UUID
		amount string
	}
	lines := make([]line, len(input.Items))
	for i, item := range input.Items {
		id, err := uuid.Parse(item.MaterialID)
		amount, numberErr := quantity.Normalize(item.Quantity, true)
		if err != nil || id == uuid.Nil || numberErr != nil {
			return nil, nil, ErrInvalid
		}
		lines[i] = line{id, amount}
	}
	slices.SortFunc(lines, func(a, b line) int { return strings.Compare(a.id.String(), b.id.String()) })
	ids := make([]uuid.UUID, len(lines))
	amounts := make([]string, len(lines))
	for i, line := range lines {
		if i > 0 && lines[i-1].id == line.id {
			return nil, nil, ErrInvalid
		}
		ids[i], amounts[i] = line.id, line.amount
	}
	return ids, amounts, nil
}
func lockMaterials(ctx context.Context, q *deliverydb.Queries, ids []uuid.UUID) error {
	locked, err := q.LockMaterials(ctx, ids)
	if err != nil {
		return err
	}
	if len(locked) != len(ids) {
		return ErrMaterial
	}
	return nil
}
func ensureInventory(ctx context.Context, q *deliverydb.Queries, store uuid.UUID, ids []uuid.UUID) error {
	if err := q.EnsureCompanyInventory(ctx, ids); err != nil {
		return err
	}
	return q.EnsureProjectInventory(ctx, deliverydb.EnsureProjectInventoryParams{StoreID: store, MaterialIds: ids})
}
func notify(ctx context.Context, tx pgx.Tx, actor uuid.UUID, project, action string) error {
	return notifications.Create(ctx, tx, notifications.Input{ProjectID: project, ActorID: actor, Kind: "delivery." + action, Title: "Delivery " + action, Message: "A delivery was " + action + " for " + project + "."}, nil)
}
func (s *Service) Create(ctx context.Context, actor uuid.UUID, input Input) (result Delivery, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx, finish := s.start(ctx, "deliveries.create", actor)
	defer func() { finish(resultErr, result.ID) }()
	ids, amounts, err := validate(input)
	if err != nil {
		return Delivery{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, err
	}
	defer rollback(tx)
	q := deliverydb.New(tx)
	if _, err := q.LockProject(ctx, input.ProjectID); err != nil {
		return Delivery{}, failure(err)
	}
	access, err := q.ProjectAccess(ctx, deliverydb.ProjectAccessParams{Actor: actor, ProjectID: input.ProjectID})
	if err != nil {
		return Delivery{}, failure(err)
	}
	company := input.Direction == "company_to_project"
	if err := authorize(access, company); err != nil {
		return Delivery{}, err
	}
	// We lock material IDs in order across projects before touching either store.
	if err := lockMaterials(ctx, q, ids); err != nil {
		return Delivery{}, err
	}
	if err := ensureInventory(ctx, q, access.StoreID, ids); err != nil {
		return Delivery{}, failure(err)
	}
	var changed int64
	if company {
		changed, err = q.SendCompanyStock(ctx, deliverydb.SendCompanyStockParams{MaterialIds: ids, Quantities: amounts})
	} else {
		changed, err = q.SendProjectStock(ctx, deliverydb.SendProjectStockParams{StoreID: access.StoreID, MaterialIds: ids, Quantities: amounts})
	}
	if err != nil {
		return Delivery{}, failure(err)
	}
	if changed != int64(len(ids)) {
		return Delivery{}, ErrStock
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Delivery{}, err
	}
	if err := q.CreateDelivery(ctx, deliverydb.CreateDeliveryParams{ID: id, ProjectID: input.ProjectID, Direction: deliverydb.SitetrackDeliveryDirection(input.Direction), InitiatedByUserID: actor}); err != nil {
		return Delivery{}, err
	}
	if err := q.CreateItems(ctx, deliverydb.CreateItemsParams{DeliveryID: id, MaterialIds: ids, Quantities: amounts}); err != nil {
		return Delivery{}, err
	}
	if err := notify(ctx, tx, actor, input.ProjectID, "sent"); err != nil {
		return Delivery{}, err
	}
	result, err = read(ctx, q, id)
	if err != nil {
		return Delivery{}, err
	}
	return result, tx.Commit(ctx)
}
func (s *Service) Transition(ctx context.Context, actor, id uuid.UUID, action string) (result Delivery, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if action != "receive" && action != "reject" && action != "cancel" {
		return Delivery{}, ErrInvalid
	}
	ctx, finish := s.start(ctx, "deliveries."+action, actor)
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
	if _, err := q.LockProject(ctx, project); err != nil {
		return Delivery{}, failure(err)
	}
	access, err := q.ProjectAccess(ctx, deliverydb.ProjectAccessParams{Actor: actor, ProjectID: project})
	if err != nil {
		return Delivery{}, failure(err)
	}
	row, err := q.LockDelivery(ctx, id)
	if err != nil {
		return Delivery{}, failure(err)
	}
	companySender := row.Direction == deliverydb.SitetrackDeliveryDirectionCompanyToProject
	companyAction := companySender == (action == "cancel")
	if err := authorize(access, companyAction); err != nil {
		return Delivery{}, err
	}
	if row.Status != deliverydb.SitetrackDeliveryStatusInTransit {
		return Delivery{}, ErrState
	}
	lines, err := q.DeliveryItems(ctx, []uuid.UUID{id})
	if err != nil {
		return Delivery{}, err
	}
	ids := make([]uuid.UUID, len(lines))
	amounts := make([]string, len(lines))
	for i, line := range lines {
		ids[i], amounts[i] = line.MaterialID, line.Quantity
	}
	if err := lockMaterials(ctx, q, ids); err != nil {
		return Delivery{}, err
	}
	// We keep outbound quantities reserved until the transaction delivers or returns them.
	returned := action != "receive"
	if companySender {
		err = q.ReleaseCompanyStock(ctx, deliverydb.ReleaseCompanyStockParams{MaterialIds: ids, Quantities: amounts, ReturnStock: returned})
	} else {
		err = q.ReleaseProjectStock(ctx, deliverydb.ReleaseProjectStockParams{StoreID: access.StoreID, MaterialIds: ids, Quantities: amounts, ReturnStock: returned})
	}
	if err != nil {
		return Delivery{}, failure(err)
	}
	status := deliverydb.SitetrackDeliveryStatusCancelled
	if action == "reject" {
		status = deliverydb.SitetrackDeliveryStatusRejected
	}
	if action == "receive" {
		status = deliverydb.SitetrackDeliveryStatusDelivered
		if companySender {
			err = q.ReceiveProjectStock(ctx, deliverydb.ReceiveProjectStockParams{StoreID: access.StoreID, MaterialIds: ids, Quantities: amounts})
		} else {
			err = q.ReceiveCompanyStock(ctx, deliverydb.ReceiveCompanyStockParams{MaterialIds: ids, Quantities: amounts})
		}
		if err != nil {
			return Delivery{}, failure(err)
		}
	}
	if err := q.CompleteDelivery(ctx, deliverydb.CompleteDeliveryParams{ID: id, Status: status, CompletedByUserID: uuid.NullUUID{UUID: actor, Valid: true}}); err != nil {
		return Delivery{}, err
	}
	if err := notify(ctx, tx, actor, project, string(status)); err != nil {
		return Delivery{}, err
	}
	result, err = read(ctx, q, id)
	if err != nil {
		return Delivery{}, err
	}
	return result, tx.Commit(ctx)
}

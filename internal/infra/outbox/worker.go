package outbox

import (
	"context"
	"errors"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"sitetrack/internal/infra/database"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
	"sitetrack/internal/infra/telemetry"
)

type Job = outboxdb.ClaimJobsRow
type Result struct{ ProviderMessageID *string }
type Handler func(context.Context, Job) (Result, error)
type Failure struct {
	Code       string
	Permanent  bool
	RetryAfter time.Duration
}

func (f *Failure) Error() string { return f.Code }

type Worker struct {
	pool        *pgxpool.Pool
	queries     *outboxdb.Queries
	handlers    map[string]Handler
	kinds       []string
	maintenance func(context.Context) error
	monitoring  *telemetry.Runtime
	outcomes    metric.Int64Counter
}

func NewWorker(pool *pgxpool.Pool, handlers map[string]Handler, maintenance func(context.Context) error, monitoring *telemetry.Runtime) (*Worker, error) {
	if pool.Config().MaxConns < 2 {
		return nil, errors.New("worker database pool requires at least two connections")
	}
	if len(handlers) == 0 && maintenance == nil {
		return nil, errors.New("outbox handlers or maintenance are required")
	}
	counter, err := monitoring.Meter.Int64Counter("outbox.processing.outcomes")
	if err != nil {
		return nil, err
	}
	w := &Worker{pool: pool, queries: outboxdb.New(pool), handlers: make(map[string]Handler), maintenance: maintenance, monitoring: monitoring, outcomes: counter}
	for kind, handler := range handlers {
		if kind == "" || handler == nil {
			return nil, errors.New("invalid outbox handler")
		}
		w.kinds = append(w.kinds, kind)
		w.handlers[kind] = handler
	}
	sort.Strings(w.kinds)
	return w, nil
}
func (w *Worker) Run(ctx context.Context) {
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		database.Listen(ctx, w.pool, "sitetrack_outbox", func(string) {
			select {
			case wake <- struct{}{}:
			default:
			}
		}, w.monitoring.Logger)
	}()
	defer func() { <-done }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var nextMaintenance time.Time
	for ctx.Err() == nil {
		if w.maintenance != nil && time.Now().After(nextMaintenance) {
			if err := w.maintenance(ctx); err != nil && ctx.Err() == nil {
				w.monitoring.Logger.ErrorContext(ctx, "worker maintenance failed")
			}
			nextMaintenance = time.Now().Add(time.Minute)
		}
		count, err := w.SendBatch(ctx)
		if err != nil && ctx.Err() == nil {
			w.monitoring.Logger.ErrorContext(ctx, "outbox processing failed")
		}
		if ctx.Err() != nil {
			return
		}
		if count > 0 && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}
func (w *Worker) SendBatch(ctx context.Context) (int, error) {
	claim, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := w.queries.ExpireJobs(claim); err != nil {
		cancel()
		return 0, err
	}
	token, err := uuid.NewV7()
	if err != nil {
		cancel()
		return 0, err
	}
	jobs, err := w.queries.ClaimJobs(claim, outboxdb.ClaimJobsParams{EventTypes: w.kinds, LeaseToken: uuid.NullUUID{UUID: token, Valid: true}})
	cancel()
	if err != nil {
		return 0, err
	}
	// We finish this claimed batch before accepting another, including during shutdown.
	failures := make(chan error, len(jobs))
	var group sync.WaitGroup
	for _, job := range jobs {
		group.Go(func() {
			if err := w.process(context.WithoutCancel(ctx), job); err != nil {
				failures <- err
			}
		})
	}
	group.Wait()
	close(failures)
	var result error
	for err := range failures {
		result = errors.Join(result, err)
	}
	return len(jobs), result
}
func (w *Worker) process(ctx context.Context, job Job) error {
	ctx, span := w.monitoring.Tracer.Start(ctx, "outbox.process", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(attribute.String("event_type", job.EventType)))
	defer span.End()
	timeout := min(10*time.Second, time.Until(job.LeaseUntil.Time)-5*time.Second)
	if timeout <= 0 {
		return nil
	}
	if job.ExpiresAt.Valid {
		timeout = min(timeout, time.Until(job.ExpiresAt.Time))
	}
	if timeout <= 0 {
		return w.retry(ctx, job, &Failure{Code: "expired", Permanent: true})
	}
	operation, cancel := context.WithTimeout(ctx, timeout)
	result, err := w.handlers[job.EventType](operation, job)
	cancel()
	if err != nil {
		span.SetStatus(codes.Error, "outbox handler failed")
		return w.retry(ctx, job, err)
	}
	ack, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	count, err := w.queries.PublishJob(ack, outboxdb.PublishJobParams{ID: job.ID, LeaseToken: job.LeaseToken, ProviderMessageID: result.ProviderMessageID})
	if err == nil && count == 1 {
		w.record(ctx, job, "published", "")
	}
	return err
}
func (w *Worker) retry(ctx context.Context, job Job, failure error) error {
	problem := &Failure{Code: "processing_failure"}
	var classified *Failure
	if errors.As(failure, &classified) {
		problem = classified
	}
	next := time.Now().Add(retryDelay(job.Attempts, problem.RetryAfter))
	status := "pending"
	if problem.Permanent || job.Attempts >= 8 || (job.ExpiresAt.Valid && !next.Before(job.ExpiresAt.Time)) {
		status = "failed"
	}
	ack, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	count, err := w.queries.RetryJob(ack, outboxdb.RetryJobParams{ID: job.ID, LeaseToken: job.LeaseToken, Status: outboxdb.SitetrackOutboxStatus(status), AvailableAt: pgtype.Timestamptz{Time: next, Valid: true}, ErrorCode: &problem.Code})
	if err == nil && count == 1 {
		w.record(ctx, job, status, problem.Code)
	}
	return err
}
func (w *Worker) record(ctx context.Context, job Job, status, code string) {
	w.outcomes.Add(ctx, 1, metric.WithAttributes(attribute.String("event_type", job.EventType), attribute.String("outcome", status)))
	w.monitoring.Logger.InfoContext(ctx, "outbox job processed", "job_id", job.ID.String(), "event_type", job.EventType, "outcome", status, "error_code", code)
}
func retryDelay(attempt int32, retryAfter time.Duration) time.Duration {
	backoff := time.Duration(1<<min(max(attempt, 1), 7)) * time.Second
	return max(backoff/2, retryAfter) + time.Duration(rand.Int64N(int64(backoff/2)+1))
}

package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	authdb "sitetrack/internal/authentication/repositories/generated"
	"sitetrack/internal/infra/email"
	"sitetrack/internal/infra/outbox"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
	"sitetrack/internal/infra/telemetry"
)

type VerificationWorker struct {
	pool       *pgxpool.Pool
	queries    *outboxdb.Queries
	accounts   *authdb.Queries
	cipher     *outbox.PayloadCipher
	sender     email.Sender
	monitoring *telemetry.Runtime
	outcomes   metric.Int64Counter
}

func NewVerificationWorker(pool *pgxpool.Pool, cipher *outbox.PayloadCipher, sender email.Sender, monitoring *telemetry.Runtime) (*VerificationWorker, error) {
	if pool.Config().MaxConns < 2 {
		return nil, errors.New("worker database pool requires at least two connections for listening and job processing")
	}
	outcomes, err := monitoring.Meter.Int64Counter("email.verification.outcomes")
	if err != nil {
		return nil, err
	}
	return &VerificationWorker{pool: pool, queries: outboxdb.New(pool), accounts: authdb.New(pool), cipher: cipher, sender: sender, monitoring: monitoring, outcomes: outcomes}, nil
}

func (w *VerificationWorker) Run(ctx context.Context) {
	wake := make(chan struct{}, 1)
	listenerDone := make(chan struct{})
	go func() { defer close(listenerDone); outbox.Listen(ctx, w.pool, wake, w.monitoring.Logger) }()
	defer func() { <-listenerDone }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var nextCleanup time.Time
	for ctx.Err() == nil {
		if time.Now().After(nextCleanup) {
			if err := w.CleanupOTPs(ctx); err != nil && ctx.Err() == nil {
				w.monitoring.Logger.ErrorContext(ctx, "OTP cleanup failed")
			}
			nextCleanup = time.Now().Add(time.Minute)
		}
		count, err := w.SendBatch(ctx)
		if err != nil && ctx.Err() == nil {
			w.monitoring.Logger.ErrorContext(ctx, "email outbox processing failed")
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

func (w *VerificationWorker) SendBatch(ctx context.Context) (int, error) {
	claim, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := w.queries.ExpireJobs(claim); err != nil {
		cancel()
		return 0, err
	}
	lease, err := uuid.NewV7()
	if err != nil {
		cancel()
		return 0, err
	}
	jobs, err := w.queries.ClaimEmails(claim, uuid.NullUUID{UUID: lease, Valid: true})
	cancel()
	if err != nil {
		return 0, err
	}
	// We drain the claimed batch before accepting another, even during shutdown.
	failures := make(chan error, len(jobs))
	var group sync.WaitGroup
	for _, job := range jobs {
		group.Go(func() {
			if err := w.send(context.WithoutCancel(ctx), job); err != nil {
				failures <- err
			}
		})
	}
	group.Wait()
	close(failures)
	var result error
	for failure := range failures {
		result = errors.Join(result, failure)
	}
	return len(jobs), result
}

func (w *VerificationWorker) send(ctx context.Context, job outboxdb.ClaimEmailsRow) error {
	payload, err := w.cipher.Open(job.Payload, job.ID[:])
	var message verificationEmailMessage
	if err != nil || json.Unmarshal(payload, &message) != nil {
		return w.retry(ctx, job, &email.DeliveryError{Code: "invalid_payload", Permanent: true})
	}
	parent := w.monitoring.Propagator.Extract(context.WithoutCancel(ctx), propagation.MapCarrier{"traceparent": message.TraceParent})
	operation, span := w.monitoring.Tracer.Start(parent, "email.verification.send", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	remainingLease := time.Until(job.LeaseUntil.Time) - 5*time.Second
	if remainingLease <= 0 {
		return nil
	}
	sendTimeout := min(10*time.Second, time.Until(job.ExpiresAt.Time), remainingLease)
	if sendTimeout <= 0 {
		return w.retry(operation, job, &email.DeliveryError{Code: "expired", Permanent: true})
	}
	send, stop := context.WithTimeout(operation, sendTimeout)
	providerID, err := w.sender.Send(send, message.Email(), "email.verification/"+job.ID.String())
	stop()
	if err != nil {
		span.SetStatus(codes.Error, "email delivery failed")
		return w.retry(operation, job, err)
	}
	ack, stop := context.WithTimeout(context.WithoutCancel(operation), 5*time.Second)
	defer stop()
	rows, err := w.queries.PublishEmail(ack, outboxdb.PublishEmailParams{ID: job.ID, LeaseToken: job.LeaseToken, ProviderMessageID: &providerID})
	if err == nil && rows == 1 {
		w.outcomes.Add(operation, 1, metric.WithAttributes(attribute.String("outcome", "published")))
		w.monitoring.Logger.InfoContext(operation, "verification email sent", "job_id", job.ID.String())
	}
	return err
}

func (w *VerificationWorker) retry(ctx context.Context, job outboxdb.ClaimEmailsRow, failure error) error {
	problem := &email.DeliveryError{Code: "delivery_failure"}
	var classified *email.DeliveryError
	if errors.As(failure, &classified) {
		problem = classified
	}
	delay := retryDelay(job.Attempts, problem.RetryAfter)
	next := time.Now().Add(delay)
	status := "pending"
	if problem.Permanent || job.Attempts >= 8 || !next.Before(job.ExpiresAt.Time) {
		status = "failed"
	}
	ack, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	rows, err := w.queries.RetryEmail(ack, outboxdb.RetryEmailParams{
		ID: job.ID, LeaseToken: job.LeaseToken, Status: status,
		AvailableAt: pgtype.Timestamptz{Time: next, Valid: true}, ErrorCode: &problem.Code,
	})
	if err == nil && rows == 1 {
		w.outcomes.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", status)))
		w.monitoring.Logger.WarnContext(ctx, "verification email delivery failed", "job_id", job.ID.String(), "outcome", status, "error_code", problem.Code)
	}
	return err
}

func retryDelay(attempt int32, retryAfter time.Duration) time.Duration {
	backoff := time.Duration(1<<min(max(attempt, 1), 7)) * time.Second
	return max(backoff/2, retryAfter) + time.Duration(rand.Int64N(int64(backoff/2)+1))
}

func (w *VerificationWorker) CleanupOTPs(ctx context.Context) error {
	cleanup, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	lease, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = w.queries.ClaimDailyCleanup(cleanup, lease)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var deleted int64
	cleanup, span := w.monitoring.Tracer.Start(cleanup, "otp.cleanup")
	defer span.End()
	for {
		count, err := w.accounts.CleanupVerifications(cleanup)
		if err != nil {
			return err
		}
		deleted += count
		if count == 0 {
			break
		}
	}
	rows, err := w.queries.CompleteDailyCleanup(cleanup, lease)
	if err == nil && rows == 1 {
		w.monitoring.Logger.InfoContext(cleanup, "daily OTP cleanup completed", "deleted", deleted)
	}
	return err
}

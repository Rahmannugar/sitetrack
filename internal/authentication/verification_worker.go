package authentication

import (
	"context"
	"encoding/json"
	"errors"
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
	queries    *outboxdb.Queries
	accounts   *authdb.Queries
	cipher     *outbox.PayloadCipher
	sender     email.Sender
	monitoring *telemetry.Runtime
	outcomes   metric.Int64Counter
}

func NewVerificationWorker(pool *pgxpool.Pool, cipher *outbox.PayloadCipher, sender email.Sender, monitoring *telemetry.Runtime) (*VerificationWorker, error) {
	outcomes, err := monitoring.Meter.Int64Counter("email.verification.outcomes")
	if err != nil {
		return nil, err
	}
	return &VerificationWorker{queries: outboxdb.New(pool), accounts: authdb.New(pool), cipher: cipher, sender: sender, monitoring: monitoring, outcomes: outcomes}, nil
}

func (w *VerificationWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var nextCleanup time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		if time.Now().After(nextCleanup) {
			if err := w.CleanupOTPs(ctx); err != nil && ctx.Err() == nil {
				w.monitoring.Logger.Error("OTP cleanup failed")
			}
			nextCleanup = time.Now().Add(time.Minute)
		}
		if err := w.SendNext(ctx); err != nil && ctx.Err() == nil {
			w.monitoring.Logger.Error("email outbox processing failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *VerificationWorker) SendNext(ctx context.Context) error {
	claim, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.queries.ExpireJobs(claim); err != nil {
		return err
	}
	lease, err := uuid.NewV7()
	if err != nil {
		return err
	}
	job, err := w.queries.ClaimEmail(claim, uuid.NullUUID{UUID: lease, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	payload, err := w.cipher.Open(job.Payload, job.ID[:])
	var message verificationEmailMessage
	if err != nil || json.Unmarshal(payload, &message) != nil {
		return w.retry(ctx, job, &email.DeliveryError{Code: "invalid_payload", Permanent: true})
	}
	parent := w.monitoring.Propagator.Extract(context.WithoutCancel(ctx), propagation.MapCarrier{"traceparent": message.TraceParent})
	operation, span := w.monitoring.Tracer.Start(parent, "email.verification.send", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	sendTimeout := min(10*time.Second, time.Until(job.ExpiresAt.Time))
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

func (w *VerificationWorker) retry(ctx context.Context, job outboxdb.ClaimEmailRow, failure error) error {
	problem := &email.DeliveryError{Code: "delivery_failure"}
	var classified *email.DeliveryError
	if errors.As(failure, &classified) {
		problem = classified
	}
	delay := max(time.Duration(1<<min(job.Attempts, 7))*time.Second, problem.RetryAfter)
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

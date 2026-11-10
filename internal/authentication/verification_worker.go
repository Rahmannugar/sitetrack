package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/codes"
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
}

func NewVerificationWorker(pool *pgxpool.Pool, cipher *outbox.PayloadCipher, sender email.Sender, monitoring *telemetry.Runtime) *VerificationWorker {
	return &VerificationWorker{queries: outboxdb.New(pool), accounts: authdb.New(pool), cipher: cipher, sender: sender, monitoring: monitoring}
}
func (w *VerificationWorker) Handle(ctx context.Context, job outbox.Job) (outbox.Result, error) {
	if w.sender == nil {
		return outbox.Result{}, &outbox.Failure{Code: "email_not_configured"}
	}
	payload, err := w.cipher.Open(job.Payload, job.ID[:])
	var message verificationEmailMessage
	if err != nil || json.Unmarshal(payload, &message) != nil {
		return outbox.Result{}, &outbox.Failure{Code: "invalid_payload", Permanent: true}
	}
	parent := w.monitoring.Propagator.Extract(ctx, propagation.MapCarrier{"traceparent": message.TraceParent})
	operation, span := w.monitoring.Tracer.Start(parent, "email.verification.send", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	id, err := w.sender.Send(operation, message.Email(), "email.verification/"+job.ID.String())
	if err != nil {
		span.SetStatus(codes.Error, "email delivery failed")
		var failure *email.DeliveryError
		if errors.As(err, &failure) {
			return outbox.Result{}, &outbox.Failure{Code: failure.Code, Permanent: failure.Permanent, RetryAfter: failure.RetryAfter}
		}
		return outbox.Result{}, &outbox.Failure{Code: "provider_transport"}
	}
	return outbox.Result{ProviderMessageID: &id}, nil
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

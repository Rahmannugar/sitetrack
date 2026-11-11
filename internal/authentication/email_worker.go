package authentication

import (
	"context"
	"errors"

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

type EmailWorker struct {
	queries    *outboxdb.Queries
	accounts   *authdb.Queries
	cipher     *outbox.PayloadCipher
	sender     email.Sender
	monitoring *telemetry.Runtime
}

func NewEmailWorker(pool *pgxpool.Pool, cipher *outbox.PayloadCipher, sender email.Sender, monitoring *telemetry.Runtime) *EmailWorker {
	return &EmailWorker{queries: outboxdb.New(pool), accounts: authdb.New(pool), cipher: cipher, sender: sender, monitoring: monitoring}
}
func (w *EmailWorker) send(ctx context.Context, job outbox.Job, message email.Message, traceParent string) (outbox.Result, error) {
	if w.sender == nil {
		return outbox.Result{}, &outbox.Failure{Code: "email_not_configured"}
	}
	parent := w.monitoring.Propagator.Extract(ctx, propagation.MapCarrier{"traceparent": traceParent})
	operation, span := w.monitoring.Tracer.Start(parent, job.EventType+".send", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	id, err := w.sender.Send(operation, message, job.EventType+"/"+job.ID.String())
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

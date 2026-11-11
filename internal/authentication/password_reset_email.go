package authentication

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/propagation"

	"sitetrack/internal/infra/email"
	"sitetrack/internal/infra/outbox"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
)

type passwordResetEmailMessage struct {
	From        string    `json:"from"`
	To          string    `json:"to"`
	Code        string    `json:"code"`
	ExpiresAt   time.Time `json:"expires_at"`
	TraceParent string    `json:"traceparent,omitempty"`
}

func (s *Service) queuePasswordReset(ctx context.Context, events *outboxdb.Queries, userID uuid.UUID, address, code string, expires time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	payload, err := json.Marshal(passwordResetEmailMessage{From: s.settings.EmailFrom, To: address, Code: code, ExpiresAt: expires, TraceParent: carrier.Get("traceparent")})
	if err != nil {
		return err
	}
	return events.Enqueue(ctx, outboxdb.EnqueueParams{ID: id, AggregateID: userID.String(), EventType: "email.password_reset",
		Payload: s.cipher.Seal(payload, id[:]), ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
}

func (w *EmailWorker) HandlePasswordReset(ctx context.Context, job outbox.Job) (outbox.Result, error) {
	payload, err := w.cipher.Open(job.Payload, job.ID[:])
	var message passwordResetEmailMessage
	if err != nil || json.Unmarshal(payload, &message) != nil {
		return outbox.Result{}, &outbox.Failure{Code: "invalid_payload", Permanent: true}
	}
	return w.send(ctx, job, email.Message{From: message.From, To: message.To, Subject: "Reset your SiteTrack password",
		Text: fmt.Sprintf("Your SiteTrack password reset code is %s. It expires at %s.", message.Code, message.ExpiresAt.UTC().Format(time.RFC3339))}, message.TraceParent)
}

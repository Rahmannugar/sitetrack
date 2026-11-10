package authentication

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Rahmannugar/authlier/emailverification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/propagation"
	"sitetrack/internal/infra/email"
	"sitetrack/internal/infra/outbox"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
)

type verificationEmailMessage struct {
	From        string    `json:"from"`
	To          string    `json:"to"`
	Code        string    `json:"code"`
	ExpiresAt   time.Time `json:"expires_at"`
	TraceParent string    `json:"traceparent,omitempty"`
}

func (m verificationEmailMessage) Email() email.Message {
	return email.Message{
		From: m.From, To: m.To, Subject: "Verify your SiteTrack email",
		Text: fmt.Sprintf("Your SiteTrack verification code is %s. It expires at %s.", m.Code, m.ExpiresAt.UTC().Format(time.RFC3339)),
	}
}

type verificationSender struct {
	events *outboxdb.Queries
	cipher *outbox.PayloadCipher
	from   string
}

func (s *verificationSender) SendVerification(ctx context.Context, message emailverification.Message) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	userID, err := uuid.Parse(message.UserID)
	if err != nil {
		return err
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	payload, err := json.Marshal(verificationEmailMessage{
		From: s.from, To: message.Email, Code: message.Code, ExpiresAt: message.ExpiresAt,
		TraceParent: carrier.Get("traceparent"),
	})
	if err != nil {
		return err
	}
	return s.events.Enqueue(ctx, outboxdb.EnqueueParams{
		ID: id, AggregateID: userID.String(), EventType: "email.verification",
		Payload: s.cipher.Seal(payload, id[:]), ExpiresAt: pgtype.Timestamptz{Time: message.ExpiresAt, Valid: true},
	})
}

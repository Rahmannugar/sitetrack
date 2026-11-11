package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"sitetrack/internal/infra/outbox"
)

func (w *EmailWorker) HandleVerification(ctx context.Context, job outbox.Job) (outbox.Result, error) {
	payload, err := w.cipher.Open(job.Payload, job.ID[:])
	var message verificationEmailMessage
	if err != nil || json.Unmarshal(payload, &message) != nil {
		return outbox.Result{}, &outbox.Failure{Code: "invalid_payload", Permanent: true}
	}
	return w.send(ctx, job, message.Email(), message.TraceParent)
}

func (w *EmailWorker) CleanupOTPs(ctx context.Context) error {
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
	for {
		count, err := w.accounts.CleanupPasswordResets(cleanup)
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

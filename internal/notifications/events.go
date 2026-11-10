package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sitetrack/internal/infra/outbox"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
	notificationdb "sitetrack/internal/notifications/repositories/generated"
)

type Event struct {
	ProjectID    string        `json:"project_id"`
	ActorID      uuid.UUID     `json:"actor_id"`
	InvitationID uuid.NullUUID `json:"invitation_id"`
	Kind         string        `json:"kind"`
	Title        string        `json:"title"`
	Message      string        `json:"message"`
}

func Enqueue(ctx context.Context, tx pgx.Tx, event Event, invitee *uuid.UUID) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err = outboxdb.New(tx).Enqueue(ctx, outboxdb.EnqueueParams{ID: id, AggregateID: event.ProjectID, EventType: "notification.project", Payload: payload}); err != nil {
		return err
	}
	q := notificationdb.New(tx)
	if invitee != nil {
		return q.CaptureInvitee(ctx, notificationdb.CaptureInviteeParams{EventID: id, UserID: *invitee})
	}
	return q.CaptureProjectRecipients(ctx, notificationdb.CaptureProjectRecipientsParams{EventID: id, ProjectID: event.ProjectID, Actor: event.ActorID})
}
func InvitationChanged(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return notificationdb.New(tx).TouchInvitationNotifications(ctx, uuid.NullUUID{UUID: id, Valid: true})
}

type Worker struct{ pool *pgxpool.Pool }

func NewWorker(pool *pgxpool.Pool) *Worker { return &Worker{pool: pool} }
func (w *Worker) Handle(ctx context.Context, job outbox.Job) (outbox.Result, error) {
	var event Event
	if json.Unmarshal(job.Payload, &event) != nil || event.ProjectID == "" || event.Kind == "" || len(event.Title) > 300 || len(event.Message) > 1000 {
		return outbox.Result{}, &outbox.Failure{Code: "invalid_notification", Permanent: true}
	}
	for {
		if err := ctx.Err(); err != nil {
			return outbox.Result{}, err
		}
		if err := w.deliver(ctx, job, event); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return outbox.Result{}, nil
			}
			return outbox.Result{}, err
		}
	}
}
func (w *Worker) deliver(ctx context.Context, job outbox.Job, event Event) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	q := notificationdb.New(tx)
	if _, err = q.LockNotificationJob(ctx, notificationdb.LockNotificationJobParams{ID: job.ID, LeaseToken: job.LeaseToken}); err != nil {
		return err
	}
	recipients, err := q.PendingRecipients(ctx, job.ID)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return pgx.ErrNoRows
	}
	ids := make([]uuid.UUID, len(recipients))
	for i := range ids {
		ids[i], err = uuid.NewV7()
		if err != nil {
			return err
		}
	}
	// We commit recipients in a stable order; retries reuse the event/recipient pair.
	if err = q.InsertNotifications(ctx, notificationdb.InsertNotificationsParams{EventID: job.ID, ProjectID: event.ProjectID, InvitationID: event.InvitationID, Type: event.Kind, Title: event.Title, Message: event.Message, Ids: ids, Users: recipients}); err != nil {
		return err
	}
	if err = q.CompleteRecipients(ctx, notificationdb.CompleteRecipientsParams{EventID: job.ID, Users: recipients}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

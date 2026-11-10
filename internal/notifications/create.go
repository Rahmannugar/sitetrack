package notifications

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	notificationdb "sitetrack/internal/notifications/repositories/generated"
)

type Input struct {
	ProjectID    string
	ActorID      uuid.UUID
	InvitationID uuid.NullUUID
	Kind         string
	Title        string
	Message      string
}

func Create(ctx context.Context, tx pgx.Tx, input Input, invitee *uuid.UUID) error {
	q := notificationdb.New(tx)
	insert := func(recipients []uuid.UUID) error {
		ids := make([]uuid.UUID, len(recipients))
		for i := range ids {
			var err error
			ids[i], err = uuid.NewV7()
			if err != nil {
				return err
			}
		}
		if err := q.LockStreams(ctx, recipients); err != nil {
			return err
		}
		return q.InsertNotifications(ctx, notificationdb.InsertNotificationsParams{ProjectID: input.ProjectID, InvitationID: input.InvitationID, Type: input.Kind, Title: input.Title, Message: input.Message, Ids: ids, Users: recipients})
	}
	if invitee != nil {
		return insert([]uuid.UUID{*invitee})
	}
	// We keep recipient locks ordered across batches within the business transaction.
	var after uuid.NullUUID
	for {
		recipients, err := q.ProjectRecipients(ctx, notificationdb.ProjectRecipientsParams{ProjectID: input.ProjectID, Actor: input.ActorID, AfterID: after})
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			return nil
		}
		if err = insert(recipients); err != nil {
			return err
		}
		after = uuid.NullUUID{UUID: recipients[len(recipients)-1], Valid: true}
	}
}
func InvitationChanged(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	q := notificationdb.New(tx)
	if err := q.LockInvitationStream(ctx, id); err != nil {
		return err
	}
	return q.TouchInvitationNotifications(ctx, uuid.NullUUID{UUID: id, Valid: true})
}

func LockProjectStreams(ctx context.Context, tx pgx.Tx, project string) error {
	return notificationdb.New(tx).LockProjectStreams(ctx, project)
}

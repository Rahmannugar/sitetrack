package projects

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"sitetrack/internal/notifications"
	projectdb "sitetrack/internal/projects/repositories/generated"
)

func invitation(row projectdb.SitetrackProjectInvitation) Invitation {
	result := Invitation{ID: row.ID, ProjectID: row.ProjectID, InviterID: row.InviterID, InviteeID: row.InviteeID, Status: row.Status, CreatedAt: row.CreatedAt.Time}
	if row.CompletedAt.Valid {
		result.CompletedAt = &row.CompletedAt.Time
	}
	return result
}
func (s *Service) Invite(ctx context.Context, actor uuid.UUID, project string, target uuid.UUID) (Invitation, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, true)
	if err != nil {
		return Invitation{}, err
	}
	defer rollback(tx)
	if target == actor || target == uuid.Nil {
		return Invitation{}, ErrInvalid
	}
	if _, err = q.RegisteredInvitee(ctx, target); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invitation{}, ErrInvitationNotFound
		}
		return Invitation{}, err
	}
	if _, err = q.GetMember(ctx, projectdb.GetMemberParams{ProjectID: project, UserID: target}); err == nil {
		return Invitation{}, ErrInvitationConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Invitation{}, err
	}
	row, err := q.CreateInvitation(ctx, projectdb.CreateInvitationParams{ID: id, ProjectID: project, InviterID: actor, InviteeID: target})
	var conflict *pgconn.PgError
	if errors.As(err, &conflict) && conflict.Code == "23505" {
		return Invitation{}, ErrInvitationConflict
	}
	if err != nil {
		return Invitation{}, err
	}
	event := notifications.Input{ProjectID: project, ActorID: actor, InvitationID: uuid.NullUUID{UUID: id, Valid: true}, Kind: "project.invitation", Title: "Project invitation", Message: "You have been invited to " + project + "."}
	if err = notifications.Create(ctx, tx, event, &target); err != nil {
		return Invitation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Invitation{}, err
	}
	return invitation(row), nil
}
func (s *Service) Withdraw(ctx context.Context, actor uuid.UUID, project string, id uuid.UUID) (Invitation, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, true)
	if err != nil {
		return Invitation{}, err
	}
	defer rollback(tx)
	existing, err := q.GetInvitation(ctx, projectdb.GetInvitationParams{ID: id, ProjectID: project})
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrInvitationNotFound
	}
	if err != nil {
		return Invitation{}, err
	}
	if existing.Status != "pending" {
		return Invitation{}, ErrInvitationConflict
	}
	row, err := q.FinishInvitation(ctx, projectdb.FinishInvitationParams{ID: id, ProjectID: project, Status: "withdrawn"})
	if err != nil {
		return Invitation{}, err
	}
	if err = notifications.InvitationChanged(ctx, tx, id); err != nil {
		return Invitation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Invitation{}, err
	}
	return invitation(row), nil
}
func (s *Service) Respond(ctx context.Context, actor, id uuid.UUID, action string) (Invitation, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if action != "accept" && action != "reject" {
		return Invitation{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invitation{}, err
	}
	defer rollback(tx)
	q := projectdb.New(tx)
	project, err := q.InvitationProject(ctx, projectdb.InvitationProjectParams{ID: id, InviteeID: actor})
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrInvitationNotFound
	}
	if err != nil {
		return Invitation{}, err
	}
	// We use the same project-first lock order as membership edits and withdrawals.
	if _, err = q.LockProject(ctx, project); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invitation{}, ErrInvitationNotFound
		}
		return Invitation{}, err
	}
	status := "rejected"
	if action == "accept" {
		status = "accepted"
	}
	row, err := q.FinishInvitation(ctx, projectdb.FinishInvitationParams{ID: id, ProjectID: project, Status: status})
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrInvitationConflict
	}
	if err != nil {
		return Invitation{}, err
	}
	if action == "accept" {
		if err = q.CreateMembership(ctx, projectdb.CreateMembershipParams{ProjectID: project, UserID: actor, IsAdmin: false}); err != nil {
			return Invitation{}, err
		}
		if err = notifications.LockProjectStreams(ctx, tx, project); err != nil {
			return Invitation{}, err
		}
		if err = notifications.Create(ctx, tx, notifications.Input{ProjectID: project, ActorID: actor, Kind: "project.member_joined", Title: "Project member joined", Message: "A new member joined " + project + "."}, nil); err != nil {
			return Invitation{}, err
		}
	}
	if err = notifications.InvitationChanged(ctx, tx, id); err != nil {
		return Invitation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Invitation{}, err
	}
	return invitation(row), nil
}

type invitationCursor struct {
	ID              uuid.UUID
	At              time.Time
	Project, Status string
}

func (s *Service) Invitations(ctx context.Context, actor uuid.UUID, project string, input InvitationListInput) (InvitationPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, project, false)
	if err != nil {
		return InvitationPage{}, err
	}
	defer rollback(tx)
	if input.Limit < 1 || input.Limit > 100 || len(input.Cursor) > 1024 {
		return InvitationPage{}, ErrInvalid
	}
	switch input.Status {
	case "pending", "accepted", "rejected", "withdrawn", "all":
	default:
		return InvitationPage{}, ErrInvalid
	}
	var after invitationCursor
	var at pgtype.Timestamptz
	var id uuid.NullUUID
	if input.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after.ID == uuid.Nil || after.At.IsZero() || after.Project != project || after.Status != input.Status {
			return InvitationPage{}, ErrInvalid
		}
		at = pgtype.Timestamptz{Time: after.At, Valid: true}
		id = uuid.NullUUID{UUID: after.ID, Valid: true}
	}
	status := input.Status
	if status == "all" {
		status = ""
	}
	rows, err := q.ListInvitations(ctx, projectdb.ListInvitationsParams{ProjectID: project, Status: status, AfterTime: at, AfterID: id, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return InvitationPage{}, err
	}
	page := InvitationPage{Items: make([]Invitation, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, invitation(row))
	}
	if more {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(invitationCursor{ID: last.ID, At: last.CreatedAt, Project: project, Status: input.Status})
		if err != nil {
			return InvitationPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, tx.Commit(ctx)
}

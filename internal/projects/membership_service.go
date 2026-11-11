package projects

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"sitetrack/internal/notifications"
	projectdb "sitetrack/internal/projects/repositories/generated"
)

func (s *Service) Members(ctx context.Context, actor uuid.UUID, id string, input MemberListInput) (MemberPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, q, err := s.locked(ctx, actor, id, false)
	if err != nil {
		return MemberPage{}, err
	}
	defer rollback(tx)
	if input.Limit < 1 || input.Limit > 100 {
		return MemberPage{}, ErrInvalid
	}
	var after uuid.NullUUID
	if input.Cursor != "" {
		value, err := uuid.Parse(input.Cursor)
		if err != nil {
			return MemberPage{}, ErrInvalid
		}
		after = uuid.NullUUID{UUID: value, Valid: true}
	}
	rows, err := q.ListMembers(ctx, projectdb.ListMembersParams{ProjectID: id, AfterID: after, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return MemberPage{}, err
	}
	page := MemberPage{Items: make([]Member, 0, input.Limit)}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, Member{UserID: row.UserID, Name: row.Name, IsAdmin: row.IsAdmin, JoinedAt: row.JoinedAt.Time})
	}
	if more {
		page.NextCursor = page.Items[len(page.Items)-1].UserID.String()
	}
	return page, tx.Commit(ctx)
}

func (s *Service) ChangeMember(ctx context.Context, actor uuid.UUID, id string, target uuid.UUID, admin *bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// We serialize membership changes on the project before checking the last admin.
	tx, q, err := s.locked(ctx, actor, id, true)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if actor == target && (admin == nil || !*admin) {
		return ErrSelfMembership
	}
	member, err := q.GetMember(ctx, projectdb.GetMemberParams{ProjectID: id, UserID: target})
	if err != nil {
		return notFound(err)
	}
	if member.IsAdmin && (admin == nil || !*admin) {
		count, err := q.CountAdmins(ctx, id)
		if err != nil {
			return err
		}
		if count <= 1 {
			return ErrLastAdmin
		}
	}
	if admin == nil {
		err = q.RemoveMember(ctx, projectdb.RemoveMemberParams{ProjectID: id, UserID: target})
	} else {
		err = q.UpdateMember(ctx, projectdb.UpdateMemberParams{ProjectID: id, UserID: target, IsAdmin: *admin})
	}
	if err != nil {
		return err
	}
	kind, title := "project.member_updated", "Project member updated"
	if admin == nil {
		kind, title = "project.member_removed", "Project member removed"
	}
	if err = notifications.Create(ctx, tx, notifications.Input{ProjectID: id, ActorID: actor, Kind: kind, Title: title, Message: "Project membership changed for " + id + "."}, nil); err != nil {
		return err
	}
	err = tx.Commit(ctx)
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.ConstraintName == "project_requires_admin" {
		return ErrLastAdmin
	}
	return err
}

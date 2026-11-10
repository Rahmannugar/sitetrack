package notifications

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"sitetrack/internal/infra/database"
	notificationdb "sitetrack/internal/notifications/repositories/generated"
	"time"
)

type Service struct {
	pool   *pgxpool.Pool
	stop   context.Context
	hub    *hub
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, stop context.Context, logger *slog.Logger) *Service {
	return &Service{pool: pool, stop: stop, hub: newHub(), logger: logger}
}
func (s *Service) Run() {
	database.Listen(s.stop, s.pool, "sitetrack_notifications", s.hub.signal, s.logger)
}

type cursor struct {
	ID uuid.UUID
	At time.Time
}

func (s *Service) List(ctx context.Context, actor uuid.UUID, input ListInput) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if input.Limit < 1 || input.Limit > 100 || len(input.Cursor) > 1024 {
		return Page{}, ErrInvalid
	}
	var after cursor
	var at pgtype.Timestamptz
	var id uuid.NullUUID
	if input.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after.ID == uuid.Nil || after.At.IsZero() {
			return Page{}, ErrInvalid
		}
		at = pgtype.Timestamptz{Time: after.At, Valid: true}
		id = uuid.NullUUID{UUID: after.ID, Valid: true}
	}
	q := notificationdb.New(s.pool)
	rows, err := q.ListNotifications(ctx, notificationdb.ListNotificationsParams{Actor: actor, AfterTime: at, AfterID: id, PageSize: int32(input.Limit + 1)})
	if err != nil {
		return Page{}, err
	}
	count, err := q.UnreadCount(ctx, actor)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]Notification, 0, input.Limit), UnreadCount: count}
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, notification(row))
	}
	if more {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(cursor{ID: last.ID, At: last.CreatedAt})
		if err != nil {
			return Page{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}
func notification(row notificationdb.ListNotificationsRow) Notification {
	result := Notification{ID: row.ID, ProjectID: row.ProjectID, Type: row.Type, Title: row.Title, Message: row.Message, CreatedAt: row.CreatedAt.Time}
	if row.ReadAt.Valid {
		result.ReadAt = &row.ReadAt.Time
	}
	if row.InvitationID.Valid && row.InvitationStatus != nil {
		result.Invitation = &Invitation{ID: row.InvitationID.UUID, Status: *row.InvitationStatus, CanRespond: *row.InvitationStatus == "pending"}
	}
	return result
}
func (s *Service) Unread(ctx context.Context, actor uuid.UUID) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return notificationdb.New(s.pool).UnreadCount(ctx, actor)
}
func (s *Service) Read(ctx context.Context, actor, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	count, err := notificationdb.New(s.pool).ReadNotification(ctx, notificationdb.ReadNotificationParams{Actor: actor, ID: id})
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) ReadAll(ctx context.Context, actor uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return notificationdb.New(s.pool).ReadAllNotifications(ctx, actor)
}

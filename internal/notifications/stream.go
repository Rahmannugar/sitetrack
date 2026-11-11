package notifications

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"sitetrack/internal/authentication"
	notificationdb "sitetrack/internal/notifications/repositories/generated"
)

type streamInput struct {
	Cursor string `json:"Last-Event-ID,omitempty" header:"Last-Event-ID" binding:"max=128"`
}
type streamState struct {
	UnreadCount int64 `json:"unread_count"`
}
type streamBatch struct {
	Items       []Notification `json:"items"`
	UnreadCount int64          `json:"unread_count"`
}

func streamCursor(revision int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(revision, 10)))
}
func parseStreamCursor(value string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, ErrInvalid
	}
	revision, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || revision < 0 {
		return 0, ErrInvalid
	}
	return revision, nil
}
func writeEvent[T any](c *gin.Context, controller *http.ResponseController, name, cursor string, value T) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if _, err = fmt.Fprintf(c.Writer, "id: %s\nevent: %s\ndata: %s\n\n", cursor, name, data); err != nil {
		return err
	}
	return controller.Flush()
}
func (s *Service) Stream(c *gin.Context, accounts authentication.Accounts, input streamInput) {
	principal, _ := authentication.CurrentPrincipal(c.Request.Context())
	actor := principal.User.ID
	wake, unsubscribe, err := s.hub.subscribe(actor)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, struct {
			Error string `json:"error"`
		}{"notification stream capacity reached"})
		return
	}
	defer unsubscribe()
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Hour)
	defer cancel()
	setup, stop := context.WithTimeout(ctx, 5*time.Second)
	head, err := notificationdb.New(s.pool).StreamHead(setup, actor)
	stop()
	if err != nil {
		notificationError(c, s.logger, err)
		return
	}
	after := head
	if input.Cursor != "" {
		after, err = parseStreamCursor(input.Cursor)
		if err != nil || after > head {
			notificationError(c, s.logger, ErrInvalid)
			return
		}
	}
	var initial Page
	var count int64
	if input.Cursor == "" {
		// We read the head before the snapshot so concurrent commits are replayed.
		initial, err = s.List(ctx, actor, ListInput{Limit: 20})
	} else {
		count, err = s.Unread(ctx, actor)
	}
	if err != nil {
		notificationError(c, s.logger, err)
		return
	}
	controller := http.NewResponseController(c.Writer)
	if err = controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		notificationError(c, s.logger, err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	if input.Cursor == "" {
		if writeEvent(c, controller, "snapshot", streamCursor(after), initial) != nil {
			return
		}
	} else {
		if writeEvent(c, controller, "state", streamCursor(after), streamState{UnreadCount: count}) != nil {
			return
		}
	}
	token := strings.Fields(c.GetHeader("Authorization"))[1]
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop.Done():
			return
		default:
		}
		if _, err = accounts.Authenticate(ctx, token); err != nil {
			if errors.Is(err, authentication.ErrUnauthenticated) {
				_ = writeEvent(c, controller, "session_expired", streamCursor(after), struct {
					Error string `json:"error"`
				}{"authentication required"})
			}
			return
		}
		read, cancel := context.WithTimeout(ctx, 5*time.Second)
		rows, err := notificationdb.New(s.pool).NotificationChanges(read, notificationdb.NotificationChangesParams{Actor: actor, AfterRevision: after})
		cancel()
		if err != nil {
			return
		}
		if len(rows) > 0 {
			items := make([]Notification, 0, len(rows))
			for _, row := range rows {
				after = row.Revision
				if row.Visible {
					items = append(items, notification(notificationdb.ListNotificationsRow{ID: row.ID, ProjectID: row.ProjectID, InvitationID: row.InvitationID, Type: row.Type, Title: row.Title, Message: row.Message, ReadAt: row.ReadAt, CreatedAt: row.CreatedAt, InvitationStatus: row.InvitationStatus, InvitationExpiresAt: row.InvitationExpiresAt}))
				}
			}
			count, err := s.Unread(ctx, actor)
			if err != nil {
				return
			}
			if writeEvent(c, controller, "notifications", streamCursor(after), streamBatch{Items: items, UnreadCount: count}) != nil {
				return
			}
			continue
		}
		count, err := s.Unread(ctx, actor)
		if err != nil {
			return
		}
		if writeEvent(c, controller, "state", streamCursor(after), streamState{UnreadCount: count}) != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-s.stop.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}

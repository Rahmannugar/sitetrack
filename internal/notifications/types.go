package notifications

import (
	"errors"
	"github.com/google/uuid"
	"time"
)

var (
	ErrInvalid  = errors.New("invalid notification request")
	ErrNotFound = errors.New("notification not found")
)

type ListInput struct {
	Cursor string `json:"cursor,omitempty" form:"cursor" binding:"max=1024"`
	Limit  int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type Invitation struct {
	ID         uuid.UUID `json:"id"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
	CanRespond bool      `json:"can_respond"`
}
type Notification struct {
	ID         uuid.UUID   `json:"id"`
	ProjectID  string      `json:"project_id"`
	Type       string      `json:"type"`
	Title      string      `json:"title"`
	Message    string      `json:"message"`
	ReadAt     *time.Time  `json:"read_at"`
	CreatedAt  time.Time   `json:"created_at"`
	Invitation *Invitation `json:"invitation,omitempty"`
}
type Page struct {
	Items       []Notification `json:"items"`
	NextCursor  string         `json:"next_cursor"`
	UnreadCount int64          `json:"unread_count"`
}

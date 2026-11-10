package projects

import (
	"errors"
	"github.com/google/uuid"
	"time"
)

var (
	ErrInvitationNotFound = errors.New("invitation not found")
	ErrInvitationConflict = errors.New("invitation is no longer pending or user already belongs to the project")
)

type InviteInput struct {
	InviteeID string `json:"invitee_id" binding:"required,uuid"`
}
type RespondInput struct {
	Action string `json:"action" binding:"required,oneof=accept reject"`
}
type InvitationListInput struct {
	Status string `json:"status,omitempty" form:"status,default=pending" binding:"oneof=pending accepted rejected withdrawn all"`
	Cursor string `json:"cursor,omitempty" form:"cursor" binding:"max=1024"`
	Limit  int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type Invitation struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   string     `json:"project_id"`
	InviterID   uuid.UUID  `json:"inviter_id"`
	InviteeID   uuid.UUID  `json:"invitee_id"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
}
type InvitationPage struct {
	Items      []Invitation `json:"items"`
	NextCursor string       `json:"next_cursor"`
}

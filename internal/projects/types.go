package projects

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound  = errors.New("project or member not found")
	ErrForbidden = errors.New("project admin required")
	ErrLastAdmin = errors.New("project must retain at least one admin")
	ErrInvalid   = errors.New("invalid project request")
)

type Input struct {
	Name      string `json:"name" binding:"required,min=2,max=150"`
	StartDate string `json:"start_date" binding:"required,datetime=2006-01-02"`
	EndDate   string `json:"end_date" binding:"required,datetime=2006-01-02"`
}
type MemberInput struct {
	IsAdmin *bool `json:"is_admin" binding:"required"`
}
type ListInput struct {
	Search string `json:"search,omitempty" form:"search" binding:"max=150"`
	Sort   string `json:"sort,omitempty" form:"sort,default=id" binding:"oneof=id name start_date"`
	Cursor string `json:"cursor,omitempty" form:"cursor" binding:"max=4096"`
	Limit  int    `json:"limit,omitempty" form:"limit,default=50" binding:"min=1,max=100"`
}
type MemberListInput struct {
	Cursor string `json:"cursor,omitempty" form:"cursor" binding:"omitempty,uuid"`
	Limit  int    `json:"limit,omitempty" form:"limit,default=50" binding:"min=1,max=100"`
}
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	StartDate   string    `json:"start_date"`
	EndDate     string    `json:"end_date"`
	StoreID     uuid.UUID `json:"store_id"`
	IsAdmin     bool      `json:"is_admin"`
	MemberCount int64     `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
}
type Page struct {
	Items      []Project `json:"items"`
	NextCursor string    `json:"next_cursor"`
}
type Member struct {
	UserID   uuid.UUID `json:"user_id"`
	Name     string    `json:"name"`
	IsAdmin  bool      `json:"is_admin"`
	JoinedAt time.Time `json:"joined_at"`
}
type MemberPage struct {
	Items      []Member `json:"items"`
	NextCursor string   `json:"next_cursor"`
}

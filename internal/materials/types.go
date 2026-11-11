package materials

import (
	"errors"
	"github.com/google/uuid"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid catalogue request")
	ErrNotFound  = errors.New("material or category not found")
	ErrForbidden = errors.New("company store manager or SuperAdmin required")
	ErrConflict  = errors.New("catalogue change conflicts with existing records")
)

type CategoryInput struct {
	Name string `json:"name" binding:"required,min=2,max=100"`
}
type Input struct {
	Name       string `json:"name" binding:"required,min=2,max=150"`
	CategoryID string `json:"category_id" binding:"required,uuid"`
	Unit       string `json:"unit" binding:"required,min=1,max=24"`
}
type ListInput struct {
	Search     string `json:"search,omitempty" form:"search" binding:"max=150"`
	CategoryID string `json:"category_id,omitempty" form:"category_id" binding:"omitempty,uuid"`
	Cursor     string `json:"cursor,omitempty" form:"cursor" binding:"max=4096"`
	Limit      int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type CategoryListInput struct {
	Search string `json:"search,omitempty" form:"search" binding:"max=100"`
	Cursor string `json:"cursor,omitempty" form:"cursor" binding:"max=4096"`
	Limit  int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type Category struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Material struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	CategoryID   uuid.UUID `json:"category_id"`
	CategoryName string    `json:"category_name"`
	Unit         string    `json:"unit"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type Page struct {
	Items      []Material `json:"items"`
	NextCursor string     `json:"next_cursor"`
}
type CategoryPage struct {
	Items      []Category `json:"items"`
	NextCursor string     `json:"next_cursor"`
}

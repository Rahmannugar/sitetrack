package deliveries

import (
	"errors"
	"github.com/google/uuid"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid delivery request")
	ErrNotFound  = errors.New("delivery or project not found")
	ErrForbidden = errors.New("store management permission required")
	ErrState     = errors.New("delivery is no longer in transit")
	ErrStock     = errors.New("insufficient stock")
	ErrCapacity  = errors.New("stock exceeds the supported quantity")
	ErrMaterial  = errors.New("material is not available")
)

type LineInput struct {
	MaterialID string `json:"material_id" binding:"required,uuid"`
	Quantity   string `json:"quantity" binding:"required,max=19"`
}
type Input struct {
	ProjectID string      `json:"project_id" binding:"required,max=32"`
	Direction string      `json:"direction" binding:"required,oneof=company_to_project project_to_company"`
	Items     []LineInput `json:"items" binding:"required,min=1,max=100,dive"`
}
type ListInput struct {
	ProjectID string `json:"project_id,omitempty" form:"project_id" binding:"max=32"`
	Direction string `json:"direction,omitempty" form:"direction" binding:"omitempty,oneof=company_to_project project_to_company"`
	Status    string `json:"status,omitempty" form:"status" binding:"omitempty,oneof=in_transit delivered rejected cancelled"`
	From      string `json:"from,omitempty" form:"from" binding:"omitempty,datetime=2006-01-02"`
	To        string `json:"to,omitempty" form:"to" binding:"omitempty,datetime=2006-01-02"`
	Cursor    string `json:"cursor,omitempty" form:"cursor" binding:"max=4096"`
	Limit     int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type Item struct {
	MaterialID uuid.UUID `json:"material_id"`
	Name       string    `json:"name"`
	Unit       string    `json:"unit"`
	Quantity   string    `json:"quantity"`
}
type Delivery struct {
	ID                uuid.UUID  `json:"id"`
	ProjectID         string     `json:"project_id"`
	ProjectName       string     `json:"project_name"`
	Direction         string     `json:"direction"`
	Status            string     `json:"status"`
	InitiatedByUserID uuid.UUID  `json:"initiated_by_user_id"`
	InitiatedByName   string     `json:"initiated_by_name"`
	CompletedByUserID *uuid.UUID `json:"completed_by_user_id"`
	CompletedByName   *string    `json:"completed_by_name"`
	CreatedAt         time.Time  `json:"created_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	Items             []Item     `json:"items"`
}
type Page struct {
	Items      []Delivery `json:"items"`
	NextCursor string     `json:"next_cursor"`
}

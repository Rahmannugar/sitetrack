package inventory

import (
	"errors"
	"github.com/google/uuid"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid inventory request")
	ErrNotFound  = errors.New("store or material not found")
	ErrForbidden = errors.New("company store manager or SuperAdmin required")
	ErrConflict  = errors.New("stock exceeds the supported quantity")
)

type QuantityInput struct {
	Quantity string `json:"quantity" binding:"required,max=19"`
}
type ReorderInput struct {
	ReorderPoint string `json:"reorder_point" binding:"required,max=19"`
}
type CompanyListInput struct {
	Search     string `json:"search,omitempty" form:"search" binding:"max=150"`
	CategoryID string `json:"category_id,omitempty" form:"category_id" binding:"omitempty,uuid"`
	Status     string `json:"status,omitempty" form:"status" binding:"omitempty,oneof=out_of_stock low_stock available"`
	Sort       string `json:"sort,omitempty" form:"sort,default=name" binding:"oneof=name quantity status"`
	Order      string `json:"order,omitempty" form:"order,default=asc" binding:"oneof=asc desc"`
	Cursor     string `json:"cursor,omitempty" form:"cursor" binding:"max=4096"`
	Limit      int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type ProjectListInput struct {
	Search     string `json:"search,omitempty" form:"search" binding:"max=150"`
	CategoryID string `json:"category_id,omitempty" form:"category_id" binding:"omitempty,uuid"`
	Sort       string `json:"sort,omitempty" form:"sort,default=name" binding:"oneof=name quantity last_delivery"`
	Order      string `json:"order,omitempty" form:"order,default=asc" binding:"oneof=asc desc"`
	Cursor     string `json:"cursor,omitempty" form:"cursor" binding:"max=4096"`
	Limit      int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type CompanyItem struct {
	MaterialID          uuid.UUID `json:"material_id"`
	Name                string    `json:"name"`
	CategoryID          uuid.UUID `json:"category_id"`
	CategoryName        string    `json:"category_name"`
	Unit                string    `json:"unit"`
	Quantity            string    `json:"quantity"`
	ReorderPoint        string    `json:"reorder_point"`
	InTransitToProjects string    `json:"in_transit_to_projects"`
	Status              string    `json:"status"`
	UpdatedAt           time.Time `json:"updated_at"`
}
type ProjectItem struct {
	MaterialID     uuid.UUID  `json:"material_id"`
	Name           string     `json:"name"`
	CategoryID     uuid.UUID  `json:"category_id"`
	CategoryName   string     `json:"category_name"`
	Unit           string     `json:"unit"`
	Quantity       string     `json:"quantity"`
	LastDeliveryAt *time.Time `json:"last_delivery_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
type CompanyPage struct {
	Items      []CompanyItem `json:"items"`
	NextCursor string        `json:"next_cursor"`
}
type ProjectPage struct {
	Items      []ProjectItem `json:"items"`
	NextCursor string        `json:"next_cursor"`
}

package tasks

import (
	"errors"
	"time"
)

var (
	ErrForbidden = errors.New("project admin required")
	ErrNotFound  = errors.New("project or task not found")
	ErrInvalid   = errors.New("invalid task request")
	ErrConflict  = errors.New("task change conflicts with current state")
)

type Input struct {
	Name      string `json:"name" binding:"required,min=2,max=150"`
	StartDate string `json:"start_date" binding:"required,datetime=2006-01-02"`
	EndDate   string `json:"end_date" binding:"required,datetime=2006-01-02"`
}
type ListInput struct {
	Search   string `json:"search,omitempty" form:"search" binding:"max=150"`
	Status   string `json:"status,omitempty" form:"status" binding:"omitempty,oneof=not_started in_progress done"`
	Late     *bool  `json:"late,omitempty" form:"late"`
	Blocked  *bool  `json:"blocked,omitempty" form:"blocked"`
	FromDate string `json:"from_date,omitempty" form:"from_date" binding:"omitempty,datetime=2006-01-02"`
	ToDate   string `json:"to_date,omitempty" form:"to_date" binding:"omitempty,datetime=2006-01-02"`
	Sort     string `json:"sort,omitempty" form:"sort,default=id" binding:"oneof=id name start_date end_date dependent_count"`
	Order    string `json:"order,omitempty" form:"order,default=asc" binding:"oneof=asc desc"`
	Cursor   string `json:"cursor,omitempty" form:"cursor" binding:"max=4096"`
	Limit    int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type Task struct {
	ID                  string    `json:"id"`
	ProjectID           string    `json:"project_id"`
	Name                string    `json:"name"`
	StartDate           string    `json:"start_date"`
	EndDate             string    `json:"end_date"`
	Status              string    `json:"status"`
	Late                bool      `json:"late"`
	Blocked             bool      `json:"blocked"`
	DependsOn           []string  `json:"depends_on"`
	DependencyCount     int64     `json:"dependency_count"`
	DependentCount      int64     `json:"dependent_count"`
	HasMoreDependencies bool      `json:"has_more_dependencies"`
	AllowedActions      []string  `json:"allowed_actions"`
	CreatedAt           time.Time `json:"created_at"`
}
type Page struct {
	Items      []Task `json:"items"`
	NextCursor string `json:"next_cursor"`
}
type DependencyInput struct {
	RequiredTaskID string `json:"required_task_id" binding:"required,max=64"`
}
type Dependency struct {
	RequiredTaskID string `json:"required_task_id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	TaskStatus     string `json:"task_status"`
}
type DependencyPage struct {
	Items      []Dependency `json:"items"`
	NextCursor string       `json:"next_cursor"`
}
type DependencyListInput struct {
	Cursor string `json:"cursor,omitempty" form:"cursor" binding:"max=1024"`
	Limit  int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type RelationshipInput struct {
	Direction string `json:"direction,omitempty" form:"direction,default=dependencies" binding:"oneof=dependencies dependents"`
	Cursor    string `json:"cursor,omitempty" form:"cursor" binding:"max=1024"`
	Limit     int    `json:"limit,omitempty" form:"limit,default=20" binding:"min=1,max=100"`
}
type Relationship struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Steps  int32  `json:"steps"`
}
type RelationshipPage struct {
	Items      []Relationship `json:"items"`
	NextCursor string         `json:"next_cursor"`
}

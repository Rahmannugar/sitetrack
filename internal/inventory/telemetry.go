package inventory

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func (s *Service) start(ctx context.Context, operation string, actor uuid.UUID) (context.Context, func(error, uuid.UUID)) {
	started := time.Now()
	ctx, span := s.monitoring.Tracer.Start(ctx, operation, trace.WithAttributes(attribute.String("actor.id", actor.String())))
	return ctx, func(err error, id uuid.UUID) {
		defer span.End()
		outcome := "success"
		if err != nil {
			outcome = "rejected"
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrConflict) {
				outcome = "failed"
				span.SetStatus(codes.Error, "inventory operation failed")
			}
		}
		if id != uuid.Nil {
			span.SetAttributes(attribute.String("material.id", id.String()))
		}
		span.SetAttributes(attribute.String("operation.outcome", outcome))
		attrs := metric.WithAttributes(attribute.String("operation", operation), attribute.String("outcome", outcome))
		s.operations.Add(ctx, 1, attrs)
		s.duration.Record(ctx, time.Since(started).Seconds(), attrs)
		switch operation {
		case "inventory.company.add_stock", "inventory.company.reorder_point":
			if err == nil {
				s.monitoring.Logger.InfoContext(ctx, "inventory changed", "operation", operation, "actor_id", actor.String(), "material_id", id.String())
			}
		}
	}
}

package telemetry

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type postgresTracer struct{ tracer trace.Tracer }

func (r *Runtime) PostgresTracer() pgx.QueryTracer { return postgresTracer{tracer: r.Tracer} }
func (t postgresTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	summary := "postgresql.query"
	first, _, _ := strings.Cut(data.SQL, "\n")
	if strings.HasPrefix(first, "-- name: ") {
		fields := strings.Fields(strings.TrimPrefix(first, "-- name: "))
		if len(fields) > 0 {
			summary = fields[0]
		}
	}
	ctx, _ = t.tracer.Start(ctx, summary, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("db.system.name", "postgresql")))
	return ctx
}
func (t postgresTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	defer span.End()
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		span.SetStatus(codes.Error, "database query failed")
	}
}

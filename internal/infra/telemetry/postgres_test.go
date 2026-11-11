package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPostgresSpansFollowParentWithoutQueryData(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer provider.Shutdown(context.Background())
	runtime := &Runtime{Tracer: provider.Tracer(scope)}
	tracer := runtime.PostgresTracer()
	secret := "password=private-value"
	ctx, parent := runtime.Tracer.Start(context.Background(), "request")
	query := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "-- name: GetMaterial :one\nSELECT $1", Args: []any{secret}})
	tracer.TraceQueryEnd(query, nil, pgx.TraceQueryEndData{Err: errors.New(secret)})
	raw := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT '" + secret + "'"})
	tracer.TraceQueryEnd(raw, nil, pgx.TraceQueryEndData{})
	parent.End()
	outside := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tracer.TraceQueryEnd(outside, nil, pgx.TraceQueryEndData{})
	spans := exporter.GetSpans()
	if len(spans) != 3 || spans[0].Name != "GetMaterial" || spans[1].Name != "postgresql.query" || spans[0].Parent.SpanID() != parent.SpanContext().SpanID() || spans[0].Status.Code != codes.Error {
		t.Fatal("database trace hierarchy is wrong", spans)
	}
	for _, span := range spans {
		if strings.Contains(span.Name, secret) || strings.Contains(span.Status.Description, secret) {
			t.Fatal("query data exposed")
		}
		for _, attribute := range span.Attributes {
			if strings.Contains(attribute.Value.Emit(), secret) {
				t.Fatal("query argument exposed")
			}
		}
		if len(span.Events) != 0 {
			t.Fatal("raw database error recorded")
		}
	}
}

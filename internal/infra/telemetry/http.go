package telemetry

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func (r *Runtime) HTTP() (gin.HandlerFunc, error) {
	duration, err := r.Meter.Float64Histogram("http.server.request.duration", metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))
	if err != nil {
		return nil, errors.New("could not create request duration metric")
	}
	active, err := r.Meter.Int64UpDownCounter("http.server.active_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, errors.New("could not create active request metric")
	}
	return func(c *gin.Context) {
		id, err := uuid.NewV7()
		if err != nil {
			r.Logger.ErrorContext(c.Request.Context(), "could not generate request ID")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		ctx := context.WithValue(c.Request.Context(), requestIDKey{}, id.String())
		c.Header("X-Request-ID", id.String())
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		method := boundedMethod(c.Request.Method)
		attrs := []attribute.KeyValue{attribute.String("http.request.method", method), attribute.String("http.route", route)}
		probe := route == "/health/live" || route == "/health/ready"
		var span trace.Span
		if !probe {
			ctx = r.Propagator.Extract(ctx, propagation.HeaderCarrier(c.Request.Header))
			ctx, span = r.Tracer.Start(ctx, method+" "+route, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attrs...))
			active.Add(ctx, 1, metric.WithAttributes(attrs...))
		}
		c.Request = c.Request.WithContext(ctx)
		started := time.Now()
		defer func() {
			panicked := false
			if recovered := recover(); recovered != nil {
				panicked = true
				// We omit panic details because they can contain request data.
				r.Logger.ErrorContext(ctx, "request panicked", "route", route)
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
				} else {
					c.Abort()
				}
			}
			status := c.Writer.Status()
			if probe && status < 500 && !panicked {
				return
			}
			resultAttrs := append(attrs, attribute.Int("http.response.status_code", status))
			duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(resultAttrs...))
			if probe {
				r.Logger.WarnContext(ctx, "health check failed", "route", route)
				return
			}
			active.Add(ctx, -1, metric.WithAttributes(attrs...))
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= 500 || panicked {
				span.SetStatus(codes.Error, "server error")
			}
			span.End()
		}()
		c.Next()
	}, nil
}

func boundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete,
		http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return method
	default:
		return "_OTHER"
	}
}

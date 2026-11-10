package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func testRuntime(t *testing.T) (*Runtime, *bytes.Buffer, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()
	var logs bytes.Buffer
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
	})
	return &Runtime{
		Logger: slog.New(correlate(slog.NewJSONHandler(&logs, nil))),
		Tracer: tp.Tracer(scope), Meter: mp.Meter(scope), Propagator: propagation.TraceContext{},
	}, &logs, exporter, reader
}

func testRouter(t *testing.T, runtime *Runtime) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	middleware, err := runtime.HTTP()
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(middleware)
	return router
}

func TestHTTPPropagatesTraceWithoutCapturingRequestData(t *testing.T) {
	runtime, logs, exporter, reader := testRuntime(t)
	router := testRouter(t, runtime)
	router.GET("/projects/:id", func(c *gin.Context) {
		runtime.Logger.InfoContext(c.Request.Context(), "project viewed")
		c.Status(http.StatusNoContent)
	})
	r := httptest.NewRequest(http.MethodGet, "/projects/private-project?token=secret-query", strings.NewReader("secret-body"))
	r.Header.Set("Authorization", "Bearer secret-auth")
	r.Header.Set("Cookie", "session=secret-cookie")
	r.Header.Set("X-Request-ID", "secret-spoofed-id")
	r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	requestID, err := uuid.Parse(w.Header().Get("X-Request-ID"))
	if err != nil || requestID.Version() != 7 {
		t.Fatal("response must contain a generated UUID v7 request ID")
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "GET /projects/:id" || spans[0].SpanKind != trace.SpanKindServer {
		t.Fatalf("unexpected request spans: %+v", spans)
	}
	if spans[0].SpanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || !spans[0].Parent.IsRemote() {
		t.Fatal("W3C parent context was not propagated")
	}
	if !strings.Contains(logs.String(), requestID.String()) || !strings.Contains(logs.String(), spans[0].SpanContext.TraceID().String()) {
		t.Fatal("application log lacks request/trace correlation")
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	observed := logs.String() + fmt.Sprintf("%+v%+v", spans, metrics)
	for _, sensitive := range []string{"secret-query", "secret-body", "secret-auth", "secret-cookie", "secret-spoofed-id", "private-project"} {
		if strings.Contains(observed, sensitive) {
			t.Fatalf("telemetry captured %s", sensitive)
		}
	}
	foundDuration := false
	for _, group := range metrics.ScopeMetrics {
		for _, item := range group.Metrics {
			if histogram, ok := item.Data.(metricdata.Histogram[float64]); ok && item.Name == "http.server.request.duration" {
				foundDuration = len(histogram.DataPoints) == 1 && histogram.DataPoints[0].Count == 1
			}
		}
	}
	if !foundDuration {
		t.Fatal("request latency was not recorded")
	}
}

func TestPanicRecoveryDoesNotDumpSecrets(t *testing.T) {
	runtime, logs, exporter, _ := testRuntime(t)
	router := testRouter(t, runtime)
	router.GET("/panic", func(_ *gin.Context) { panic("secret-panic") })
	r := httptest.NewRequest(http.MethodGet, "/panic?key=secret-query", nil)
	r.Header.Set("Authorization", "Bearer secret-auth")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "request panicked") {
		t.Fatal("panic did not produce a safe error response and operational log")
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error {
		t.Fatal("panic was not recorded as a failed request span")
	}
	for _, sensitive := range []string{"secret-panic", "secret-query", "secret-auth"} {
		if strings.Contains(logs.String()+w.Body.String()+fmt.Sprint(spans), sensitive) {
			t.Fatal("panic recovery leaked request data")
		}
	}
}

func TestHealthChecksSuppressSuccessAndRecordFailure(t *testing.T) {
	runtime, logs, exporter, reader := testRuntime(t)
	router := testRouter(t, runtime)
	status := http.StatusOK
	router.GET("/health/ready", func(c *gin.Context) { c.Status(status) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	if len(exporter.GetSpans()) != 0 || logs.Len() != 0 || len(metrics.ScopeMetrics) != 0 {
		t.Fatal("successful probes must not produce logs, spans or metric data")
	}
	status = http.StatusServiceUnavailable
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "health check failed") || len(metrics.ScopeMetrics) == 0 {
		t.Fatal("readiness failure must remain observable")
	}
}

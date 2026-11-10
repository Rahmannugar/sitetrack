package telemetry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"sitetrack/internal/config"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestShutdownExportsAllSignalsWithSharedIdentity(t *testing.T) {
	var mu sync.Mutex
	payloads := make(map[string][]byte)
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("api-key") != "test-license-key" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Error("OTLP request lacks license authentication or protobuf content type")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		payloads[r.URL.Path] = body
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/x-protobuf"}},
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	})}
	var output bytes.Buffer
	runtime, err := newRuntime(context.Background(), config.Telemetry{
		Enabled: true, Environment: "test", Endpoint: "https://collector.example",
		APIKey: "test-license-key", SampleRatio: 1,
	}, "sitetrack-api", &output, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := runtime.Tracer.Start(context.Background(), "project.create")
	runtime.Logger.InfoContext(ctx, "project created")
	counter, err := runtime.Meter.Int64Counter("project.created")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 1)
	span.End()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var traces collectortrace.ExportTraceServiceRequest
	var metrics collectormetrics.ExportMetricsServiceRequest
	var logs collectorlogs.ExportLogsServiceRequest
	for path, message := range map[string]proto.Message{
		"/v1/traces": &traces, "/v1/metrics": &metrics, "/v1/logs": &logs,
	} {
		payload, found := payloads[path]
		if !found {
			t.Fatalf("shutdown did not flush %s", path)
		}
		if err := proto.Unmarshal(payload, message); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	if len(traces.ResourceSpans) != 1 || len(metrics.ResourceMetrics) != 1 || len(logs.ResourceLogs) != 1 {
		t.Fatal("missing exported signal resource")
	}
	resources := [][]*common.KeyValue{
		traces.ResourceSpans[0].Resource.Attributes,
		metrics.ResourceMetrics[0].Resource.Attributes,
		logs.ResourceLogs[0].Resource.Attributes,
	}
	instance := ""
	for _, attrs := range resources {
		values := make(map[string]string)
		for _, attr := range attrs {
			values[attr.Key] = attr.Value.GetStringValue()
		}
		if values["service.name"] != "sitetrack-api" || values["deployment.environment.name"] != "test" {
			t.Fatalf("signal identity mismatch: %v", values)
		}
		if instance == "" {
			instance = values["service.instance.id"]
		}
		if instance == "" || values["service.instance.id"] != instance {
			t.Fatal("signals must share one instance identity")
		}
	}
	logRecord := logs.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	traceRecord := traces.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if !bytes.Equal(logRecord.TraceId, traceRecord.TraceId) || !bytes.Equal(logRecord.SpanId, traceRecord.SpanId) {
		t.Fatal("exported logs are not correlated to their trace")
	}
	if strings.Contains(output.String(), "test-license-key") {
		t.Fatal("license key leaked to stdout")
	}
}

func TestDisabledExportKeepsLocalLogging(t *testing.T) {
	var output bytes.Buffer
	runtime, err := newRuntime(context.Background(), config.Telemetry{}, "sitetrack-api", &output,
		&http.Client{Transport: transportFunc(func(_ *http.Request) (*http.Response, error) {
			t.Error("disabled telemetry attempted an export")
			return nil, context.Canceled
		})})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Logger.Info("API starting", "event", "startup")
	if err := runtime.Shutdown(context.Background()); err != nil || !strings.Contains(output.String(), "API starting") {
		t.Fatal("disabled export must preserve local logs and shut down cleanly")
	}
}

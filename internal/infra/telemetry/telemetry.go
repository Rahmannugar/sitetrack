package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"sitetrack/internal/config"
)

const scope = "sitetrack"

type Runtime struct {
	Logger     *slog.Logger
	Tracer     trace.Tracer
	Meter      metric.Meter
	Propagator propagation.TextMapPropagator
	shutdown   []func(context.Context) error
}

func New(ctx context.Context, cfg config.Telemetry, service string, output io.Writer) (*Runtime, error) {
	return newRuntime(ctx, cfg, service, output, &http.Client{
		Timeout: 5 * time.Second,
		// We reject redirects so the ingest key stays on the configured endpoint.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	})
}

func newRuntime(ctx context.Context, cfg config.Telemetry, service string, output io.Writer, client *http.Client) (_ *Runtime, resultErr error) {
	stdout := slog.NewJSONHandler(output, nil)
	runtime := &Runtime{
		Logger:     slog.New(correlate(stdout)).With("service", service),
		Tracer:     tracenoop.NewTracerProvider().Tracer(scope),
		Meter:      metricnoop.NewMeterProvider().Meter(scope),
		Propagator: propagation.TraceContext{},
	}
	if !cfg.Enabled {
		return runtime, nil
	}
	// We log export failures locally to avoid sending them through a failing exporter.
	fallback := slog.New(stdout).With("service", service)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(_ error) {
		fallback.Warn("telemetry export failed")
	}))
	defer func() {
		if resultErr != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = runtime.Shutdown(shutdown)
		}
	}()
	instanceID, err := uuid.NewV7()
	if err != nil {
		return nil, errors.New("could not generate telemetry instance ID")
	}
	res := resource.NewSchemaless(
		attribute.String("service.name", service),
		attribute.String("service.instance.id", instanceID.String()),
		attribute.String("deployment.environment.name", cfg.Environment),
	)
	headers := map[string]string{"api-key": cfg.APIKey}
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	traceExporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint+"/v1/traces"),
		otlptracehttp.WithHeaders(headers), otlptracehttp.WithHTTPClient(client))
	if err != nil {
		return nil, errors.New("could not initialize trace export")
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
		sdktrace.WithBatcher(traceExporter, sdktrace.WithMaxQueueSize(2048), sdktrace.WithExportTimeout(5*time.Second)))
	runtime.shutdown = append(runtime.shutdown, tp.Shutdown)
	runtime.Tracer = tp.Tracer(scope)

	metricExporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(endpoint+"/v1/metrics"),
		otlpmetrichttp.WithHeaders(headers), otlpmetrichttp.WithHTTPClient(client),
		otlpmetrichttp.WithTemporalitySelector(func(kind sdkmetric.InstrumentKind) metricdata.Temporality {
			switch kind {
			case sdkmetric.InstrumentKindCounter, sdkmetric.InstrumentKindObservableCounter, sdkmetric.InstrumentKindHistogram:
				return metricdata.DeltaTemporality
			default:
				return metricdata.CumulativeTemporality
			}
		}))
	if err != nil {
		return nil, errors.New("could not initialize metric export")
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(
		sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(30*time.Second), sdkmetric.WithTimeout(5*time.Second))))
	runtime.shutdown = append(runtime.shutdown, mp.Shutdown)
	runtime.Meter = mp.Meter(scope)

	logExporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(endpoint+"/v1/logs"),
		otlploghttp.WithHeaders(headers), otlploghttp.WithHTTPClient(client))
	if err != nil {
		return nil, errors.New("could not initialize log export")
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(
		sdklog.NewBatchProcessor(logExporter, sdklog.WithMaxQueueSize(2048), sdklog.WithExportTimeout(5*time.Second))))
	runtime.shutdown = append(runtime.shutdown, lp.Shutdown)
	runtime.Logger = slog.New(correlate(slog.NewMultiHandler(stdout,
		otelslog.NewHandler(scope, otelslog.WithLoggerProvider(lp))))).With("service", service)
	return runtime, nil
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	var result error
	for _, shutdown := range r.shutdown {
		result = errors.Join(result, shutdown(ctx))
	}
	return result
}

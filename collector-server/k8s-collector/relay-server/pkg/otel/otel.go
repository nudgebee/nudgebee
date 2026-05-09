package otel

import (
	"context"
	"log/slog"
	"nudgebee/relay-server/pkg/config"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/metric"
	noopMetrics "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	noopTrace "go.opentelemetry.io/otel/trace/noop"

	"google.golang.org/grpc"
)

type urlFilterTraceSampler struct {
	description string
}

func (ts urlFilterTraceSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	psc := trace.SpanContextFromContext(p.ParentContext)
	for _, attr := range p.Attributes {
		if attr.Key == "http.url" && attr.Value.AsString() == "/status" {
			return sdktrace.SamplingResult{
				Decision:   sdktrace.Drop,
				Tracestate: psc.TraceState(),
			}
		}
	}
	return sdktrace.SamplingResult{
		Decision:   sdktrace.RecordAndSample,
		Tracestate: psc.TraceState(),
	}
}

func (ts urlFilterTraceSampler) Description() string {
	return ts.description
}

func newTracerProvider(logger slog.Logger, config config.Config, res *resource.Resource) (trace.TracerProvider, error) {
	logger.Info("otel traces exporter", "exporter", config.Otel.Exporter, "endpoint", config.Otel.ExporterOtlpEndpoint)

	if config.Otel.Exporter == "console" {
		exporter, err := stdouttrace.New()
		if err != nil {
			return nil, err
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithSampler(urlFilterTraceSampler{}),
		)
		return tp, nil
	} else if config.Otel.TracesExporter == "otlp" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Otel.GrpcTimeoutSeconds)*time.Second)
		defer cancel()
		exporter, err := otlptracegrpc.New(
			ctx,
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(config.Otel.ExporterOtlpTracesEndpoint),
			otlptracegrpc.WithDialOption(grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(config.Otel.GrpcMaxMsgSize),
				grpc.MaxCallSendMsgSize(config.Otel.GrpcMaxMsgSize),
			)),
		)
		if err != nil {
			return nil, err
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithSampler(urlFilterTraceSampler{}),
		)
		return tp, nil
	} else {
		tp := noopTrace.NewTracerProvider()
		return tp, nil
	}
}

func newMeterProvider(logger slog.Logger, config config.Config, res *resource.Resource) (metric.MeterProvider, error) {
	logger.Info("otel metrics exporter", "exporter", config.Otel.MetricesExporter, "endpoint", config.Otel.ExporterOtlpMetricsEndpoint)
	switch config.Otel.MetricesExporter {
	case "console":
		metricExporter, err := stdoutmetric.New()
		if err != nil {
			return nil, err
		}

		meterProvider := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
				sdkmetric.WithInterval(1*time.Minute))),
		)
		return meterProvider, nil
	case "otlp":
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Otel.GrpcTimeoutSeconds)*time.Second)
		defer cancel()
		metricExporter, err := otlpmetricgrpc.New(
			ctx,
			otlpmetricgrpc.WithInsecure(),
			otlpmetricgrpc.WithEndpoint(config.Otel.ExporterOtlpMetricsEndpoint),
			otlpmetricgrpc.WithDialOption(grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(config.Otel.GrpcMaxMsgSize),
				grpc.MaxCallSendMsgSize(config.Otel.GrpcMaxMsgSize),
			)),
		)
		if err != nil {
			return nil, err
		}

		meterProvider := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
				sdkmetric.WithInterval(1*time.Minute))),
		)
		return meterProvider, nil
	default:
		meterProvider := noopMetrics.NewMeterProvider()
		return meterProvider, nil
	}
}

func newResource(config config.Config) (*resource.Resource, error) {
	additionalAttributes := []attribute.KeyValue{}
	if os.Getenv("OTEL_RESOURCE_ATTRIBUTES") != "" {
		attributes := strings.Split(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), ",")
		for _, attr := range attributes {
			if strings.Contains(attr, "=") {
				kv := strings.Split(attr, "=")
				additionalAttributes = append(additionalAttributes, attribute.String(kv[0], kv[1]))
			} else {
				additionalAttributes = append(additionalAttributes, attribute.String(attr, ""))
			}
		}
	}

	additionalAttributes = append(additionalAttributes, attribute.String("service.name", config.Otel.ServiceName))
	additionalAttributes = append(additionalAttributes, attribute.String("service.version", "0.1.0"))
	additionalAttributes = append(additionalAttributes, attribute.String("telemetry.sdk.language", "go"))
	additionalAttributes = append(additionalAttributes, attribute.String("telemetry.distro.name", goruntime.GOOS))
	additionalAttributes = append(additionalAttributes, attribute.String("telemetry.sdk.version", runtime.Version))
	additionalAttributes = append(additionalAttributes, attribute.String("process.runtime.version", goruntime.Version()))

	return resource.NewSchemaless(additionalAttributes...), nil
}

func InitOtel(logger slog.Logger, config config.Config) (trace.TracerProvider, metric.MeterProvider, error) {
	res, err := newResource(config)
	if err != nil {
		panic(err)
	}

	tp, err := newTracerProvider(logger, config, res)
	if err != nil {
		return nil, nil, err
	}
	mp, err := newMeterProvider(logger, config, res)
	if err != nil {
		return nil, nil, err
	}
	otel.SetMeterProvider(mp)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	//start runtime metrics collection
	if config.Otel.MetricesExporter == "otlp" || config.Otel.MetricesExporter == "console" {
		err = runtime.Start(runtime.WithMinimumReadMemStatsInterval(1 * time.Minute))
		if err != nil {
			logger.Error("failed to init metric otel", "error", err)
		}
	}

	eh := errorHandler{logger: logger}
	otel.SetErrorHandler(eh)
	return tp, mp, nil
}

type errorHandler struct {
	logger slog.Logger
}

func (r errorHandler) Handle(err error) {
	r.logger.Error("otel error", "error", err)
}

package main

import (
	"context"
	"log"
	"log/slog"
	"nudgebee/tickets-server/common"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/go-logr/stdr"
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
		if attr.Key == "http.url" && attr.Value.AsString() == "/health" {
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

func newTracerProvider(res *resource.Resource) (trace.TracerProvider, error) {
	slog.Info("otel traces exporter", "exporter", common.Config.OtelTracesExporter, "endpoint", common.Config.OtelExporterOtlpTracesEndpoint)

	switch common.Config.OtelTracesExporter {
	case "console":
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
	case "otlp":
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(common.Config.OtelGrpcTimeoutSeconds)*time.Second)
		defer cancel()
		exporter, err := otlptracegrpc.New(
			ctx,
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(common.Config.OtelExporterOtlpTracesEndpoint),
			otlptracegrpc.WithDialOption(grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(common.Config.OtelGrpcMaxMsgSize),
				grpc.MaxCallSendMsgSize(common.Config.OtelGrpcMaxMsgSize),
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
	default:
		tp := noopTrace.NewTracerProvider()
		return tp, nil
	}
}

func newMeterProvider(res *resource.Resource) (metric.MeterProvider, error) {
	slog.Info("otel metrics exporter", "exporter", common.Config.OtelMetricesExporter, "endpoint", common.Config.OtelExporterOtlpMetricsEndpoint)
	switch common.Config.OtelMetricesExporter {
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
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(common.Config.OtelGrpcTimeoutSeconds)*time.Second)
		defer cancel()
		metricExporter, err := otlpmetricgrpc.New(
			ctx,
			otlpmetricgrpc.WithInsecure(),
			otlpmetricgrpc.WithEndpoint(common.Config.OtelExporterOtlpMetricsEndpoint),
			otlpmetricgrpc.WithDialOption(grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(common.Config.OtelGrpcMaxMsgSize),
				grpc.MaxCallSendMsgSize(common.Config.OtelGrpcMaxMsgSize),
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

func newResource() (*resource.Resource, error) {
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

	additionalAttributes = append(additionalAttributes, attribute.String("service.name", common.SERVICE_NAME))
	additionalAttributes = append(additionalAttributes, attribute.String("service.version", "0.1.0"))
	additionalAttributes = append(additionalAttributes, attribute.String("telemetry.sdk.language", "go"))
	additionalAttributes = append(additionalAttributes, attribute.String("telemetry.distro.name", goruntime.GOOS))
	additionalAttributes = append(additionalAttributes, attribute.String("telemetry.sdk.version", runtime.Version))
	additionalAttributes = append(additionalAttributes, attribute.String("process.runtime.version", goruntime.Version()))

	return resource.NewSchemaless(additionalAttributes...), nil
}

func initOtel() (trace.TracerProvider, metric.MeterProvider, error) {
	res, err := newResource()
	if err != nil {
		panic(err)
	}

	tp, err := newTracerProvider(res)
	if err != nil {
		return nil, nil, err
	}
	mp, err := newMeterProvider(res)
	if err != nil {
		return nil, nil, err
	}
	otel.SetMeterProvider(mp)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	//start runtime metrics collection
	err = runtime.Start(runtime.WithMinimumReadMemStatsInterval(1 * time.Minute))
	if err != nil {
		log.Fatal(err)
	}

	eh := errorHandler{}
	otel.SetErrorHandler(eh)

	if strings.ToLower(os.Getenv("OTEL_LOG_LEVEL")) == "debug" {
		slog.Info("setting otel log level to debug")
		stdr.SetVerbosity(100)
	}

	return tp, mp, nil
}

type errorHandler struct {
}

func (r errorHandler) Handle(err error) {
	slog.Error("otel error", "error", err)
}

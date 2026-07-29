package main

import (
	"context"
	"log"
	"log/slog"
	goruntime "runtime"
	"strings"
	"time"

	"nudgebee/llm-gateway/config"

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

// urlFilterTraceSampler drops spans for the /health probe to avoid trace noise.
type urlFilterTraceSampler struct{ description string }

func (ts urlFilterTraceSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	psc := trace.SpanContextFromContext(p.ParentContext)
	// The path attribute key varies by instrumentation/semconv version (http.target,
	// url.path, http.route) and http.url may be an absolute URL, so match any of them
	// by suffix so the /health probe is reliably dropped.
	for _, attr := range p.Attributes {
		switch attr.Key {
		case "http.target", "url.path", "http.route", "http.url":
			if v := attr.Value.AsString(); v == "/health" || strings.HasSuffix(v, "/health") {
				return sdktrace.SamplingResult{Decision: sdktrace.Drop, Tracestate: psc.TraceState()}
			}
		}
	}
	return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample, Tracestate: psc.TraceState()}
}

func (ts urlFilterTraceSampler) Description() string { return ts.description }

func newTracerProvider(res *resource.Resource) (trace.TracerProvider, error) {
	slog.Info("otel: traces exporter", "exporter", config.Config.OtelTracesExporter, "endpoint", config.Config.OtelExporterOtlpTracesEndpoint)
	switch config.Config.OtelTracesExporter {
	case "console":
		exporter, err := stdouttrace.New()
		if err != nil {
			return nil, err
		}
		return sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithSampler(urlFilterTraceSampler{}),
		), nil
	case "otlp":
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.OtelGrpcTimeoutSeconds)*time.Second)
		defer cancel()
		exporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(config.Config.OtelExporterOtlpTracesEndpoint),
			otlptracegrpc.WithDialOption(grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(config.Config.OtelGrpcMaxMsgSize),
				grpc.MaxCallSendMsgSize(config.Config.OtelGrpcMaxMsgSize),
			)),
		)
		if err != nil {
			return nil, err
		}
		return sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithSampler(urlFilterTraceSampler{}),
		), nil
	default:
		return noopTrace.NewTracerProvider(), nil
	}
}

func newMeterProvider(res *resource.Resource) (metric.MeterProvider, error) {
	slog.Info("otel: metrics exporter", "exporter", config.Config.OtelMetricsExporter, "endpoint", config.Config.OtelExporterOtlpMetricsEndpoint)
	switch config.Config.OtelMetricsExporter {
	case "console":
		exp, err := stdoutmetric.New()
		if err != nil {
			return nil, err
		}
		return sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(30*time.Second))),
		), nil
	case "otlp":
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.OtelGrpcTimeoutSeconds)*time.Second)
		defer cancel()
		exp, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithInsecure(),
			otlpmetricgrpc.WithEndpoint(config.Config.OtelExporterOtlpMetricsEndpoint),
			otlpmetricgrpc.WithDialOption(grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(config.Config.OtelGrpcMaxMsgSize),
				grpc.MaxCallSendMsgSize(config.Config.OtelGrpcMaxMsgSize),
			)),
		)
		if err != nil {
			return nil, err
		}
		return sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(30*time.Second))),
		), nil
	default:
		return noopMetrics.NewMeterProvider(), nil
	}
}

func newResource() *resource.Resource {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", config.SERVICE_NAME),
		attribute.String("service.version", "0.1.0"),
		attribute.String("telemetry.sdk.language", "go"),
		attribute.String("process.runtime.version", goruntime.Version()),
	}
	return resource.NewSchemaless(attrs...)
}

// initOtel wires trace + metric providers per config. Returns the providers so
// main can shut them down cleanly. Exporter "" => noop (safe local default).
func initOtel() (trace.TracerProvider, metric.MeterProvider, error) {
	res := newResource()
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

	if config.Config.OtelMetricsExporter == "console" || config.Config.OtelMetricsExporter == "otlp" {
		if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Minute)); err != nil {
			log.Fatal(err)
		}
	}
	otel.SetErrorHandler(errorHandler{})
	if strings.ToLower(config.Config.LogLevel) == "debug" {
		stdr.SetVerbosity(100)
	}
	return tp, mp, nil
}

type errorHandler struct{}

func (errorHandler) Handle(err error) { slog.Error("otel: error", "error", err) }

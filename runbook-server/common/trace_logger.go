package common

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// LoggerWithTrace stamps the active trace_id / span_id from ctx onto the
// logger so its lines correlate with the rest of the platform in Loki. Only
// when a real span is present — a background context returns the logger
// unchanged rather than emitting an all-zero trace_id. Use for loggers built
// before a security.RequestContext exists (e.g. MQ consumer preambles);
// NewRequestContext stamps its own logger.
func LoggerWithTrace(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		return logger.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	return logger
}

package common

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// TraceLogger returns slog.Default() stamped with trace_id/span_id from the
// context's span, so lines logged in plain-context code paths (worker pools,
// provider clients) correlate with the rest of the platform in Loki. Only
// when a real span is present — a background context returns the default
// logger untouched rather than emitting an all-zero trace_id.
func TraceLogger(ctx context.Context) *slog.Logger {
	logger := slog.Default()
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		logger = logger.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	return logger
}

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

// TraceLogHandler wraps a slog.Handler and appends trace_id / span_id from the
// record's context to every log line emitted through the slog.XxxContext
// variants (slog.InfoContext, slog.ErrorContext, ...). Lines logged without a
// context (plain slog.Info) pass through unchanged, as do lines whose context
// carries no valid span. Loggers already stamped via With (NewRequestContext /
// LoggerWithTrace) call the non-context variants, so they don't double-stamp.
type TraceLogHandler struct {
	slog.Handler
}

func NewTraceLogHandler(inner slog.Handler) *TraceLogHandler {
	return &TraceLogHandler{Handler: inner}
}

func (h *TraceLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		// Clone before modifying — the slog.Handler contract forbids mutating
		// a Record that may be shared with other handlers.
		record = record.Clone()
		record.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, record)
}

func (h *TraceLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TraceLogHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *TraceLogHandler) WithGroup(name string) slog.Handler {
	return &TraceLogHandler{Handler: h.Handler.WithGroup(name)}
}

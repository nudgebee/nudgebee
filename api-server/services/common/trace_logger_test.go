package common

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

// spanContext builds a valid remote span context for tests.
func spanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	require.NoError(t, err)
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
}

func logLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	return line
}

func TestLoggerWithTrace(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))

	t.Run("stamps trace and span ids when span is valid", func(t *testing.T) {
		buf.Reset()
		ctx := trace.ContextWithSpanContext(context.Background(), spanContext(t))
		LoggerWithTrace(ctx, base).Info("hello")
		line := logLine(t, &buf)
		assert.Equal(t, "0123456789abcdef0123456789abcdef", line["trace_id"])
		assert.Equal(t, "0123456789abcdef", line["span_id"])
	})

	t.Run("background context leaves logger unchanged", func(t *testing.T) {
		buf.Reset()
		LoggerWithTrace(context.Background(), base).Info("hello")
		line := logLine(t, &buf)
		assert.NotContains(t, line, "trace_id")
	})

	t.Run("nil logger falls back to default without panicking", func(t *testing.T) {
		assert.NotNil(t, LoggerWithTrace(context.Background(), nil))
	})
}

func TestTraceLogHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewTraceLogHandler(slog.NewJSONHandler(&buf, nil)))

	t.Run("context variant stamps trace and span ids", func(t *testing.T) {
		buf.Reset()
		ctx := trace.ContextWithSpanContext(context.Background(), spanContext(t))
		logger.InfoContext(ctx, "hello")
		line := logLine(t, &buf)
		assert.Equal(t, "0123456789abcdef0123456789abcdef", line["trace_id"])
		assert.Equal(t, "0123456789abcdef", line["span_id"])
	})

	t.Run("plain variant passes through unchanged", func(t *testing.T) {
		buf.Reset()
		logger.Info("hello")
		line := logLine(t, &buf)
		assert.NotContains(t, line, "trace_id")
	})

	t.Run("span-less context passes through unchanged", func(t *testing.T) {
		buf.Reset()
		logger.InfoContext(context.Background(), "hello")
		line := logLine(t, &buf)
		assert.NotContains(t, line, "trace_id")
	})

	t.Run("WithAttrs and WithGroup keep stamping", func(t *testing.T) {
		buf.Reset()
		ctx := trace.ContextWithSpanContext(context.Background(), spanContext(t))
		logger.With("k", "v").InfoContext(ctx, "hello")
		line := logLine(t, &buf)
		assert.Equal(t, "v", line["k"])
		assert.Equal(t, "0123456789abcdef0123456789abcdef", line["trace_id"])
	})
}

package common

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(NewDedupeHandler(slog.NewJSONHandler(buf, nil)))
}

func TestDedupeHandler_NestedWithKeepsOneKey(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	for i := 0; i < 7; i++ {
		logger = logger.With("trace_id", "t1", "span_id", "s1", "file", "f.go", "line", i)
	}
	logger.Info("hello", "line", 99)

	out := buf.String()
	for _, key := range []string{`"trace_id"`, `"span_id"`, `"file"`, `"line"`} {
		assert.Equal(t, 1, strings.Count(out, key), "key %s in %s", key, out)
	}
	var m map[string]any
	require.NoError(t, UnmarshalJson(buf.Bytes(), &m))
	assert.Equal(t, float64(99), m["line"], "last value wins")
}

func TestDedupeHandler_KeepsErrorText(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).Error("failed", "error", errors.New("boom"))
	assert.Contains(t, buf.String(), `"error":"boom"`)
}

func TestDedupeHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).With("a", 1, "a", 2).WithGroup("g").With("b", 1, "b", 2).Info("m", "c", 3)

	var m map[string]any
	require.NoError(t, UnmarshalJson(buf.Bytes(), &m))
	assert.Equal(t, float64(2), m["a"])
	assert.Equal(t, map[string]any{"b": float64(2), "c": float64(3)}, m["g"])
	assert.Equal(t, 1, strings.Count(buf.String(), `"b"`))
}

func TestDedupeHandler_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewDedupeHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	logger.Info("dropped")
	assert.Empty(t, buf.String())
}

func TestDedupeHandler_InlineGroupsKept(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).Info("m", slog.Group("", slog.Int("a", 1)), slog.Group("", slog.Int("b", 2)))

	var m map[string]any
	require.NoError(t, UnmarshalJson(buf.Bytes(), &m))
	assert.Equal(t, float64(1), m["a"])
	assert.Equal(t, float64(2), m["b"])
}

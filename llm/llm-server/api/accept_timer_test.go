package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAcceptTimerLogsEachStepAndTotal(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	timer := newAcceptTimer()
	time.Sleep(5 * time.Millisecond)
	timer.mark("budget")
	timer.mark("routing")
	timer.log(logger, "/v1/completions/chat")

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	require.Equal(t, "api: chat accept timing", line["msg"])
	require.Equal(t, "/v1/completions/chat", line["endpoint"])
	require.GreaterOrEqual(t, line["budget_ms"], float64(5))
	require.Contains(t, line, "routing_ms")
	require.GreaterOrEqual(t, line["total_ms"], line["budget_ms"])
}

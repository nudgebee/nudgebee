package tools

import (
	"testing"
	"time"

	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One resolver reads a canonical log query's window for both the plan
// validation and the tool that runs the query. These are the forms it reads.
func TestResolveLogQueryWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	at := func(s string) time.Time {
		parsed, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return parsed
	}
	query := func(command string) core.QueryBuilder {
		built, err := core.BuildLogQueryBuilder(core.NbToolContext{}, command)
		require.NoError(t, err)
		return built
	}

	for name, tc := range map[string]struct {
		command string
		args    map[string]any
		want    LogQueryWindow
	}{
		"start and end": {
			command: `{"where":{},"start_time":"2026-10-01T07:00:00Z","end_time":"2026-10-01T09:00:00Z"}`,
			want:    LogQueryWindow{Start: at("2026-10-01T07:00:00Z"), End: at("2026-10-01T09:00:00Z")},
		},
		"start and end beat a range beside them": {
			command: `{"where":{},"start_time":"2026-10-01T07:00:00Z","end_time":"2026-10-01T09:00:00Z","time_range":"24h"}`,
			want:    LogQueryWindow{Start: at("2026-10-01T07:00:00Z"), End: at("2026-10-01T09:00:00Z")},
		},
		"a bare range ends now": {
			command: `{"where":{},"time_range":"30m"}`,
			want:    LogQueryWindow{Start: now.Add(-30 * time.Minute), End: now, EndsNow: true, Range: 30 * time.Minute},
		},
		"a range with an end is anchored there": {
			command: `{"where":{},"time_range":"30m","end_time":"2026-09-20T10:00:00Z"}`,
			want:    LogQueryWindow{Start: at("2026-09-20T09:30:00Z"), End: at("2026-09-20T10:00:00Z")},
		},
		"a start alone runs to now": {
			command: `{"where":{},"start_time":"2026-10-01T08:00:00Z"}`,
			want:    LogQueryWindow{Start: at("2026-10-01T08:00:00Z"), End: now, EndsNow: true},
		},
		"an ISO interval in the range slot": {
			command: `{"where":{},"time_range":"2026-10-01T07:00:00Z/2026-10-01T09:00:00Z"}`,
			want:    LogQueryWindow{Start: at("2026-10-01T07:00:00Z"), End: at("2026-10-01T09:00:00Z")},
		},
		"timestamp bounds in the filter": {
			command: `{"where":{"timestamp":{"_gte":"2026-10-01T07:00:00Z","_lte":"2026-10-01T09:00:00Z"}}}`,
			want:    LogQueryWindow{Start: at("2026-10-01T07:00:00Z"), End: at("2026-10-01T09:00:00Z")},
		},
		"a window control wins over bounds in the filter": {
			command: `{"where":{"timestamp":{"_gte":"2026-10-01T07:00:00Z","_lte":"2026-10-01T09:00:00Z"}},"time_range":"30m"}`,
			want:    LogQueryWindow{Start: now.Add(-30 * time.Minute), End: now, EndsNow: true, Range: 30 * time.Minute},
		},
		"controls passed beside the query win over its own": {
			command: `{"where":{},"time_range":"24h"}`,
			args:    map[string]any{"range": "15m"},
			want:    LogQueryWindow{Start: now.Add(-15 * time.Minute), End: now, EndsNow: true, Range: 15 * time.Minute},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := ResolveLogQueryWindow(query(tc.command), tc.args, now)
			require.True(t, ok)
			assert.True(t, tc.want.Start.Equal(got.Start), "start: want %s got %s", tc.want.Start, got.Start)
			assert.True(t, tc.want.End.Equal(got.End), "end: want %s got %s", tc.want.End, got.End)
			assert.Equal(t, tc.want.EndsNow, got.EndsNow)
			assert.Equal(t, tc.want.Range, got.Range)
		})
	}

	for name, command := range map[string]string{
		"no window at all":       `{"where":{"app":{"_eq":"api"}},"limit":100}`,
		"a range that is no use": `{"where":{},"time_range":"soon"}`,
		"a start in the future":  `{"where":{},"start_time":"2026-10-02T08:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := ResolveLogQueryWindow(query(command), nil, now)
			assert.False(t, ok)
		})
	}
}

// The tool runs a query over the window the resolver reads. A start with no
// end used to fall through to the last hour.
func TestNBLogToolV2_Call_RunsOverTheQuerysWindow(t *testing.T) {
	logProvider := services_server.ObservabilityProvider{Provider: "loki"}
	run := func(t *testing.T, command string) (startMs, endMs int64) {
		t.Helper()
		var captured map[string]any
		cleanup := startLogsStubCapture(t, `{"logs":[],"query":"{app=\"api\"}","provider":"loki"}`, &captured)
		defer cleanup()
		tool := &NBLogToolV2{accountId: "acc-1", logProvider: logProvider}
		_, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: command})
		require.NoError(t, err)
		return int64(captured["start_time"].(float64)), int64(captured["end_time"].(float64))
	}
	ms := func(s string) int64 {
		parsed, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return parsed.UnixMilli()
	}

	t.Run("start and end", func(t *testing.T) {
		start, end := run(t, `{"where":{"app":{"_eq":"api"}},"start_time":"2026-10-01T07:00:00Z","end_time":"2026-10-01T09:00:00Z","time_range":"24h"}`)
		assert.Equal(t, ms("2026-10-01T07:00:00Z"), start)
		assert.Equal(t, ms("2026-10-01T09:00:00Z"), end)
	})

	t.Run("a start alone runs to now", func(t *testing.T) {
		from := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
		start, end := run(t, `{"where":{"app":{"_eq":"api"}},"start_time":"`+from.Format(time.RFC3339)+`"}`)
		assert.Equal(t, from.UnixMilli(), start)
		assert.InDelta(t, time.Now().UnixMilli(), end, 5000)
	})

	t.Run("no window is the last hour", func(t *testing.T) {
		start, end := run(t, `{"where":{"app":{"_eq":"api"}}}`)
		assert.InDelta(t, time.Now().Add(-time.Hour).UnixMilli(), start, 5000)
		assert.InDelta(t, time.Now().UnixMilli(), end, 5000)
	})
}

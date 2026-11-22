package tools

import (
	"context"
	"io"
	"log/slog"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExecuteCliCommand(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)

	toolContext := core.NbToolContext{
		Ctx: reqCtx,
	}

	t.Run("single allowed command", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "echo hello", nil, []string{"echo"})
		assert.NoError(t, err)
		assert.Equal(t, "hello\n", output)
	})

	t.Run("single disallowed command", func(t *testing.T) {
		_, err := ExecuteCliCommand(toolContext, "ls -l", nil, []string{"echo"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "command 'ls' is not allowed")
	})

	t.Run("multiple allowed commands with pipe", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "echo 'hello world' | wc -c", nil, []string{"echo", "wc"})
		assert.NoError(t, err)
		assert.Equal(t, "12", strings.TrimSpace(output))
	})

	t.Run("multiple commands with one disallowed", func(t *testing.T) {
		_, err := ExecuteCliCommand(toolContext, "echo hello | grep h", nil, []string{"echo", "wc"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "command 'grep' is not allowed")
	})

	t.Run("no allowed commands specified", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "echo hello", nil, []string{})
		assert.NoError(t, err)
		assert.Equal(t, "hello\n", output)
	})

	t.Run("nil allowed commands slice", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "echo hello", nil, nil)
		assert.NoError(t, err)
		assert.Equal(t, "hello\n", output)
	})

	t.Run("command with path", func(t *testing.T) {
		_, err := ExecuteCliCommand(toolContext, "/bin/ls", nil, []string{"ls"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "command '/bin/ls' is not allowed")
	})

	t.Run("allowed command with path", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "/bin/echo hello", nil, []string{"/bin/echo"})
		assert.NoError(t, err)
		assert.Equal(t, "hello\n", output)
	})

	t.Run("command substitution with $()", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "echo $(rm -rf /)", nil, []string{"echo", "rm"})
		assert.NoError(t, err)
		assert.Equal(t, "$(rm -rf /)\n", output)
	})

	t.Run("command substitution with backticks", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "echo `rm -rf /`", nil, []string{"echo", "rm"})
		assert.NoError(t, err)
		assert.Equal(t, "`rm -rf /`\n", output)
	})

	t.Run("command chaining with &&", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "echo hello && echo world", nil, []string{"echo"})
		assert.NoError(t, err)
		assert.Equal(t, "hello && echo world\n", output)
	})

	t.Run("command chaining with ;", func(t *testing.T) {
		output, err := ExecuteCliCommand(toolContext, "echo hello ; echo world", nil, []string{"echo"})
		assert.NoError(t, err)
		assert.Equal(t, "hello ; echo world\n", output)
	})
}

func TestConvertCsvToJsonString(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	toolContext := core.NbToolContext{
		Ctx: reqCtx,
	}
	t.Run("valid csv", func(t *testing.T) {
		csvData := "header1,header2\nvalue1,value2"
		expectedJson := `[{"header1":"value1","header2":"value2"}]`
		jsonOutput := convertCsvToJsonString(toolContext, csvData, ',')
		assert.JSONEq(t, expectedJson, jsonOutput)
	})
}

func TestConfigValueExists(t *testing.T) {
	configs := []core.ToolConfigValue{
		{Name: "config1", Value: "value1"},
		{Name: "config2", Value: ""},
	}
	assert.True(t, configValueExists(configs, "config1"))
	assert.False(t, configValueExists(configs, "config2"))
	assert.False(t, configValueExists(configs, "config3"))
}

func TestIsErrorLogLine(t *testing.T) {
	assert.True(t, IsErrorLogLine("this is an error message"))
	assert.True(t, IsErrorLogLine("[E] something went wrong"))
	assert.True(t, IsErrorLogLine("W: a warning"))
	assert.False(t, IsErrorLogLine("this is just a normal log"))
}

func TestClassifyLogSeverity(t *testing.T) {
	tests := []struct {
		name     string
		log      string
		expected LogSeverity
	}{
		{"error keyword", "ERROR: something went wrong", LogSeverityError},
		{"panic keyword", "PANIC: unexpected nil", LogSeverityError},
		{"fatal keyword", "FATAL: cannot continue", LogSeverityError},
		{"error bracket", "[ERROR] something", LogSeverityError},
		{"single E", "E something went wrong", LogSeverityError},
		{"warning keyword", "WARNING: disk space low", LogSeverityWarning},
		{"single W", "W something unusual", LogSeverityWarning},
		{"warning bracket", "[W] something unusual", LogSeverityWarning},
		{"info line", "INFO: all is well", LogSeverityNone},
		{"plain log", "just a normal log line", LogSeverityNone},
		{"error takes priority over warning", "ERROR: warning about something", LogSeverityError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ClassifyLogSeverity(tt.log))
		})
	}
}

func TestNormalizeLogLine(t *testing.T) {
	assert.Equal(t, "a log line", normalizeLogLine("  a log line  "))
}

func TestGetErrorLinesFromLogStringOrDefault(t *testing.T) {
	log := "INFO: line1\nERROR: line2\nINFO: line3\nWARNING: line4"
	expected := []string{"ERROR: line2", "INFO: line3", "WARNING: line4"}
	assert.Equal(t, expected, GetErrorLinesFromLogStringOrDefault(log, false))
}

func TestParseUnixTimestamp(t *testing.T) {
	sec := int64(1678886400)
	assert.Equal(t, time.Unix(sec, 0).UTC(), parseUnixTimestamp(sec).UTC())
}

func TestParseTimeValue(t *testing.T) {
	rfcNano := "2023-03-15T12:00:00.123Z"
	t1, _ := time.Parse(time.RFC3339Nano, rfcNano)
	p1, err := parseTimeValue(rfcNano)
	assert.NoError(t, err)
	assert.True(t, t1.Equal(p1))
}

func TestExtractStartEndtimeFromLabels(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	toolContext := core.NbToolContext{
		Ctx: reqCtx,
	}
	labels := map[string]any{
		"start_time": "2023-01-01T00:00:00Z",
		"end_time":   "2023-01-01T01:00:00Z",
	}
	start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
	assert.NoError(t, err)
	assert.Equal(t, "2023-01-01T00:00:00Z", start.Format(time.RFC3339))
	assert.Equal(t, "2023-01-01T01:00:00Z", end.Format(time.RFC3339))
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		hasError bool
	}{
		{"1h", time.Hour, false},
		{"2d", 48 * time.Hour, false},
		{"1w", 7 * 24 * time.Hour, false},
		{"1m", time.Minute, false},
		{"1mo", 30 * 24 * time.Hour, false},
		{"1y", 365 * 24 * time.Hour, false},
		{"invalid", 0, true},
		{"1.5d", 36 * time.Hour, false},
		{"2 weeks", 14 * 24 * time.Hour, false},
		{"1 month", 30 * 24 * time.Hour, false},
		{"  2d  ", 48 * time.Hour, false},
		{"0.5d", 12 * time.Hour, false},
	}

	for _, tt := range tests {
		result, err := ParseDuration(tt.input)
		if tt.hasError {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		}
	}
}

func TestExtractStartEndtimeFromLabels_Range(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	toolContext := core.NbToolContext{
		Ctx: reqCtx,
	}

	t.Run("range in days", func(t *testing.T) {
		labels := map[string]any{
			"range": "2d",
		}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		// Allow small difference due to time.Now execution time
		diff := end.Sub(start)
		expected := 48 * time.Hour
		assert.InDelta(t, expected.Seconds(), diff.Seconds(), 1.0)
	})

	t.Run("range with explicit end time", func(t *testing.T) {
		endTimeStr := "2023-01-05T00:00:00Z"
		labels := map[string]any{
			"range":    "1d",
			"end_time": endTimeStr,
		}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		assert.Equal(t, endTimeStr, end.Format(time.RFC3339))
		assert.Equal(t, "2023-01-04T00:00:00Z", start.Format(time.RFC3339))
	})

	// The investigation asked for "75m from 10:30" on a day-old incident; anchoring
	// the range to now queried the last 75 minutes instead and found nothing.
	t.Run("range with explicit start time runs forward from the start", func(t *testing.T) {
		labels := map[string]any{
			"start_time": "2023-01-05T10:30:00Z",
			"range":      "75m",
		}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		assert.Equal(t, "2023-01-05T10:30:00Z", start.Format(time.RFC3339))
		assert.Equal(t, "2023-01-05T11:45:00Z", end.Format(time.RFC3339))
	})

	t.Run("range from a recent start stops at now", func(t *testing.T) {
		startTime := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
		labels := map[string]any{
			"start_time": startTime.Format(time.RFC3339),
			"range":      "1h",
		}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		assert.True(t, start.Equal(startTime))
		assert.WithinDuration(t, time.Now(), end, 2*time.Second)
	})

	t.Run("a negative range means the same span back", func(t *testing.T) {
		labels := map[string]any{
			"range":    "-1h",
			"end_time": "2023-01-05T00:00:00Z",
		}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		assert.Equal(t, "2023-01-04T23:00:00Z", start.Format(time.RFC3339))
		assert.Equal(t, "2023-01-05T00:00:00Z", end.Format(time.RFC3339))
	})

	t.Run("an explicit end still wins over the start", func(t *testing.T) {
		labels := map[string]any{
			"start":    "2023-01-01T00:00:00Z",
			"end_time": "2023-01-05T00:00:00Z",
			"range":    "1d",
		}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		assert.Equal(t, "2023-01-04T00:00:00Z", start.Format(time.RFC3339))
		assert.Equal(t, "2023-01-05T00:00:00Z", end.Format(time.RFC3339))
	})

	// The logs agent writes absolute windows as an ISO-8601 interval. It is not a
	// duration, so it used to fall through to the default last hour and the agent
	// counted logs from the wrong window.
	t.Run("an ISO-8601 interval is the window itself", func(t *testing.T) {
		labels := map[string]any{"range": "2026-09-25T18:18:00Z/2026-09-25T18:48:00Z"}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		assert.Equal(t, "2026-09-25T18:18:00Z", start.Format(time.RFC3339))
		assert.Equal(t, "2026-09-25T18:48:00Z", end.Format(time.RFC3339))
	})

	t.Run("a reversed interval is swapped", func(t *testing.T) {
		labels := map[string]any{"range": "2026-09-25T18:48:00Z / 2026-09-25T18:18:00Z"}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		assert.Equal(t, "2026-09-25T18:18:00Z", start.Format(time.RFC3339))
		assert.Equal(t, "2026-09-25T18:48:00Z", end.Format(time.RFC3339))
	})

	t.Run("an unparseable interval is not a window", func(t *testing.T) {
		_, _, err := ExtractStartEndtimeFromLabels(toolContext, map[string]any{"range": "yesterday/today"})
		assert.Error(t, err)
	})
}

// The logs agent sometimes writes its window into the filter instead of time_range. With no
// window argument the search ran over the last hour and answered 0 for 18:18–18:48 yesterday.
func TestWindowFromTimestampBounds(t *testing.T) {
	now := time.Date(2026, 9, 26, 3, 0, 0, 0, time.UTC)
	b := func(op core.BinaryWhereClauseType, v any) core.QueryWhereClause {
		return core.QueryWhereClause{Binary: core.BinaryWhereClause{"timestamp": {op: v}}}
	}

	t.Run("bounds under _and are the window", func(t *testing.T) {
		from, to, ok := windowFromTimestampBounds(core.QueryWhereClause{And: []core.QueryWhereClause{
			{Binary: core.BinaryWhereClause{"app": {core.Eq: "llm-server"}}},
			b(core.Gte, "2026-09-25T18:18:00Z"),
			b(core.Lte, "2026-09-25T18:48:00Z"),
		}}, now)
		assert.True(t, ok)
		assert.Equal(t, "2026-09-25T18:18:00Z", from.Format(time.RFC3339))
		assert.Equal(t, "2026-09-25T18:48:00Z", to.Format(time.RFC3339))
	})

	t.Run("the tightest bounds win", func(t *testing.T) {
		from, to, ok := windowFromTimestampBounds(core.QueryWhereClause{And: []core.QueryWhereClause{
			b(core.Gt, "2026-09-25T18:00:00Z"), b(core.Gte, "2026-09-25T18:18:00Z"),
			b(core.Lt, "2026-09-25T19:00:00Z"), b(core.Lte, "2026-09-25T18:48:00Z"),
		}}, now)
		assert.True(t, ok)
		assert.Equal(t, "2026-09-25T18:18:00Z", from.Format(time.RFC3339))
		assert.Equal(t, "2026-09-25T18:48:00Z", to.Format(time.RFC3339))
	})

	t.Run("a lone lower bound runs to now; a lone upper bound covers the hour before", func(t *testing.T) {
		from, to, ok := windowFromTimestampBounds(b(core.Gte, "2026-09-26T01:00:00Z"), now)
		assert.True(t, ok)
		assert.Equal(t, "2026-09-26T01:00:00Z", from.Format(time.RFC3339))
		assert.Equal(t, now, to)

		from, to, ok = windowFromTimestampBounds(b(core.Lt, "2026-09-26T01:00:00Z"), now)
		assert.True(t, ok)
		assert.Equal(t, "2026-09-26T00:00:00Z", from.Format(time.RFC3339))
		assert.Equal(t, "2026-09-26T01:00:00Z", to.Format(time.RFC3339))
	})

	t.Run("bounds inside _or or _not do not constrain the window", func(t *testing.T) {
		_, _, ok := windowFromTimestampBounds(core.QueryWhereClause{
			Or:  []core.QueryWhereClause{b(core.Gte, "2026-09-25T18:18:00Z")},
			Not: &core.QueryWhereClause{Binary: core.BinaryWhereClause{"timestamp": {core.Lte: "2026-09-25T18:48:00Z"}}},
		}, now)
		assert.False(t, ok)
	})

	t.Run("no timestamp bound, an inverted pair, or a future lower bound is no window", func(t *testing.T) {
		_, _, ok := windowFromTimestampBounds(core.QueryWhereClause{Binary: core.BinaryWhereClause{"app": {core.Eq: "x"}}}, now)
		assert.False(t, ok)
		_, _, ok = windowFromTimestampBounds(core.QueryWhereClause{And: []core.QueryWhereClause{
			b(core.Gte, "2026-09-25T19:00:00Z"), b(core.Lte, "2026-09-25T18:00:00Z")}}, now)
		assert.False(t, ok)
		_, _, ok = windowFromTimestampBounds(b(core.Gte, "2026-09-27T00:00:00Z"), now)
		assert.False(t, ok)
	})

	t.Run("an explicit window argument is detected", func(t *testing.T) {
		assert.True(t, hasWindowArgs(map[string]any{"range": "1h"}))
		assert.True(t, hasWindowArgs(map[string]any{"start_time": "x"}))
		assert.False(t, hasWindowArgs(map[string]any{"index": "logs-*"}))
	})
}

func TestExtractStartEndtimeFromLabels_MergePriority(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)

	t.Run("start after end gets swapped", func(t *testing.T) {
		toolContext := core.NbToolContext{Ctx: reqCtx}
		labels := map[string]any{
			"start": float64(1774333866844), // end
			"end":   float64(1774333837000), // start (swapped)
		}
		start, end, err := ExtractStartEndtimeFromLabels(toolContext, labels)
		assert.NoError(t, err)
		assert.True(t, end.After(start), "end should be after start after swap")
	})

	t.Run("LLM start_time overrides event label start epoch", func(t *testing.T) {
		toolCtxWithLabels := core.NbToolContext{
			Ctx: reqCtx,
			QueryConfig: core.NBQueryConfig{
				Labels: map[string]any{
					"start": float64(1774333837000), // event epoch (29s window)
					"end":   float64(1774333866844),
				},
			},
		}
		labels := map[string]any{
			"start_time": "2026-03-11T07:45:00Z",
			"end_time":   "2026-03-11T08:45:00Z",
		}
		start, end, err := ExtractStartEndtimeFromLabels(toolCtxWithLabels, labels)
		assert.NoError(t, err)
		assert.Equal(t, "2026-03-11T07:45:00Z", start.Format(time.RFC3339), "LLM start_time should take priority over event labels")
		assert.Equal(t, "2026-03-11T08:45:00Z", end.Format(time.RFC3339), "LLM end_time should take priority over event labels")
	})

	t.Run("event labels used as fallback when LLM provides no time", func(t *testing.T) {
		toolCtxWithLabels := core.NbToolContext{
			Ctx: reqCtx,
			QueryConfig: core.NBQueryConfig{
				Labels: map[string]any{
					"start": float64(1774333837000),
					"end":   float64(1774333866844),
				},
			},
		}
		labels := map[string]any{} // No LLM-provided times
		start, end, err := ExtractStartEndtimeFromLabels(toolCtxWithLabels, labels)
		assert.NoError(t, err)
		// Returns the raw 29s window — expansion is done by ExpandNarrowTimeWindow in log tools
		assert.InDelta(t, 29.844, end.Sub(start).Seconds(), 1.0, "should return raw event window")
	})

	// Regression: an explicit LLM-provided range (e.g. {"time_range":"1h"} in the
	// query JSON) must win over the event's narrow start/end window. Before the
	// caller-first resolution this lost, because the event's start/end pair was
	// checked before the range branch within the merged label set.
	t.Run("LLM range overrides event label start/end epoch", func(t *testing.T) {
		toolCtxWithLabels := core.NbToolContext{
			Ctx: reqCtx,
			QueryConfig: core.NBQueryConfig{
				Labels: map[string]any{
					"start": float64(1774333837000), // event epoch (29s window)
					"end":   float64(1774333866844),
				},
			},
		}
		labels := map[string]any{"range": "1h"} // LLM asked for the last hour
		start, end, err := ExtractStartEndtimeFromLabels(toolCtxWithLabels, labels)
		assert.NoError(t, err)
		assert.InDelta(t, time.Hour.Seconds(), end.Sub(start).Seconds(), 1.0,
			"explicit LLM range should win over the narrow event window")
	})
}

// TestExpandNarrowTimeWindow tests the log-specific time window expansion helper.
// This is separate from ExtractStartEndtimeFromLabels because expansion is a
// log-specific heuristic, not appropriate for events/metrics/traces tools.
func TestExpandNarrowTimeWindow(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("29s PagerDuty window expands to 30m", func(t *testing.T) {
		start := time.UnixMilli(1774333837000)
		end := time.UnixMilli(1774333866844)
		s, e := ExpandNarrowTimeWindow(logger, start, end)
		assert.Equal(t, 30*time.Minute, e.Sub(s), "29s window should expand to 30m")
	})

	t.Run("zero duration expands to 30m", func(t *testing.T) {
		ts := time.UnixMilli(1774333837000)
		s, e := ExpandNarrowTimeWindow(logger, ts, ts)
		assert.Equal(t, 30*time.Minute, e.Sub(s))
	})

	t.Run("4m59s window expands to 30m", func(t *testing.T) {
		start, _ := time.Parse(time.RFC3339, "2023-06-15T10:00:00Z")
		end, _ := time.Parse(time.RFC3339, "2023-06-15T10:04:59Z")
		s, e := ExpandNarrowTimeWindow(logger, start, end)
		assert.Equal(t, 30*time.Minute, e.Sub(s))
	})

	t.Run("5m window stays unchanged", func(t *testing.T) {
		start, _ := time.Parse(time.RFC3339, "2023-06-15T10:00:00Z")
		end, _ := time.Parse(time.RFC3339, "2023-06-15T10:05:00Z")
		s, e := ExpandNarrowTimeWindow(logger, start, end)
		assert.Equal(t, start, s)
		assert.Equal(t, end, e)
	})

	t.Run("1h window stays unchanged", func(t *testing.T) {
		start, _ := time.Parse(time.RFC3339, "2023-01-01T00:00:00Z")
		end, _ := time.Parse(time.RFC3339, "2023-01-01T01:00:00Z")
		s, e := ExpandNarrowTimeWindow(logger, start, end)
		assert.Equal(t, start, s)
		assert.Equal(t, end, e)
	})

	t.Run("24h window stays unchanged", func(t *testing.T) {
		start, _ := time.Parse(time.RFC3339, "2023-01-01T00:00:00Z")
		end, _ := time.Parse(time.RFC3339, "2023-01-02T00:00:00Z")
		s, e := ExpandNarrowTimeWindow(logger, start, end)
		assert.Equal(t, 24*time.Hour, e.Sub(s))
	})
}

func TestForGithubAgentExecuteCli(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected bool
		reason   string
	}{
		{
			name:     "gh with --jq flag",
			command:  "gh pr list --jq '.[] | .number'",
			expected: true,
			reason:   "GitHub CLI with --jq flag should be treated specially",
		},
		{
			name:     "gh with --jq= flag",
			command:  "gh issue view 123 --jq='.title'",
			expected: true,
			reason:   "GitHub CLI with --jq= format should be treated specially",
		},
		{
			name:     "gh piped to jq",
			command:  "gh pr list --json number,title | jq '.[] | select(.number > 100)'",
			expected: true,
			reason:   "GitHub CLI piped to jq should be treated specially",
		},
		{
			name:     "gh piped to multiple commands with jq",
			command:  "gh api repos/owner/repo/pulls | jq '.[] | .id' | sort",
			expected: true,
			reason:   "GitHub CLI in pipeline with jq should be treated specially",
		},
		{
			name:     "gh with full path and jq pipe",
			command:  "/usr/bin/gh pr list | jq '.[]'",
			expected: true,
			reason:   "Full path to gh with jq pipe should be treated specially",
		},
		{
			name:     "gh without jq",
			command:  "gh pr list --json number,title",
			expected: false,
			reason:   "GitHub CLI without jq should use normal pipeline splitting",
		},
		{
			name:     "gh piped to grep",
			command:  "gh pr list | grep 'feature'",
			expected: false,
			reason:   "GitHub CLI piped to non-jq commands should use normal pipeline",
		},
		{
			name:     "gh piped to awk then jq",
			command:  "gh pr list | awk '{print $1}' | jq '.'",
			expected: true,
			reason:   "Pipeline contains jq even if not immediate next command",
		},
		{
			name:     "non-gh command with jq",
			command:  "curl https://api.github.com/repos | jq '.[]'",
			expected: false,
			reason:   "Non-gh command should not trigger special handling",
		},
		{
			name:     "non-gh command with --jq flag",
			command:  "kubectl get pods --jq '.items[]'",
			expected: false,
			reason:   "Non-gh command with --jq should not trigger special handling",
		},
		{
			name:     "empty command",
			command:  "",
			expected: false,
			reason:   "Empty command should return false",
		},
		{
			name:     "gh with whitespace and jq",
			command:  "gh pr list   |   jq '.[]'",
			expected: true,
			reason:   "Extra whitespace should be handled correctly",
		},
		{
			name:     "gh with empty pipe segment",
			command:  "gh pr list || jq '.[]'",
			expected: true,
			reason:   "Empty pipe segments should be skipped",
		},
		{
			name:     "gh with multiple pipes and jq in middle",
			command:  "gh pr list | jq '.[]' | head -n 10",
			expected: true,
			reason:   "jq anywhere in pipeline should trigger special handling",
		},
		{
			name:     "gh workflow with --jq and complex query",
			command:  "gh workflow list --jq '.workflows[] | select(.state == \"active\") | .name'",
			expected: true,
			reason:   "Complex --jq query with real-world workflow filtering",
		},
		{
			name:     "gh api call with jq for issue extraction",
			command:  "gh api repos/nudgebee/platform/issues | jq '[.[] | {number, title, state}]'",
			expected: true,
			reason:   "Common pattern: API call with jq to extract issue details",
		},
		{
			name:     "gh pr view with --jq for status check",
			command:  "gh pr view 42 --json statusCheckRollup --jq '.statusCheckRollup[] | select(.conclusion == \"FAILURE\")'",
			expected: true,
			reason:   "Check PR status failures with --jq filter",
		},
		{
			name:     "gh release list piped to jq for version parsing",
			command:  "gh release list --json tagName,createdAt | jq '.[] | select(.tagName | startswith(\"v1.\"))'",
			expected: true,
			reason:   "Filter releases by version prefix using jq",
		},
		{
			name:     "gh without jq for simple listing",
			command:  "gh pr list --state open --limit 10",
			expected: false,
			reason:   "Simple PR listing without JSON processing",
		},
		{
			name:     "gh run list for CI monitoring",
			command:  "gh run list --workflow=ci.yml --status=failure --limit 5",
			expected: false,
			reason:   "Monitor failed CI runs without jq processing",
		},
		{
			name:     "git command with gh-like syntax",
			command:  "git log --oneline | jq '.'",
			expected: false,
			reason:   "git command should not trigger gh special handling",
		},
		{
			name:     "gh command with redirect",
			command:  "gh pr list > output.txt",
			expected: false,
			reason:   "Output redirect without jq should use normal handling",
		},
		{
			name:     "glab mr list with --jq containing a pipe",
			command:  "glab mr list --output json --jq '.[] | select(.state == \"opened\") | .title'",
			expected: true,
			reason:   "glab exposes its own --jq; the expression's pipes must not be split into stages",
		},
		{
			name:     "glab ci list piped to jq",
			command:  "glab ci list --status failed --output json | jq '.[0].id'",
			expected: true,
			reason:   "jq anywhere in a glab pipeline should trigger the same handling as gh",
		},
		{
			name:     "glab without jq",
			command:  "glab mr list --per-page 10",
			expected: false,
			reason:   "Simple glab listing without JSON processing",
		},
		{
			name:     "non-git CLI with jq is unaffected",
			command:  "kubectl get pods -o json | jq '.items[].metadata.name'",
			expected: false,
			reason:   "Only gh and glab get the jq special case",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isGitCliCommandWithJq(tt.command)
			assert.Equal(t, tt.expected, result, "Reason: %s", tt.reason)
		})
	}
}

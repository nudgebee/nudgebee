package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestCountLogsInObservabilityResponse(t *testing.T) {
	t.Run("valid response with logs", func(t *testing.T) {
		resp := core.ObservabilityLogResponse{
			Logs: []core.ObservabilityLog{
				{Message: "log 1"},
				{Message: "log 2"},
				{Message: "log 3"},
			},
		}
		respBytes, _ := json.Marshal(resp)
		assert.Equal(t, 3, CountLogsInObservabilityResponse(string(respBytes)))
	})

	t.Run("empty logs", func(t *testing.T) {
		resp := core.ObservabilityLogResponse{Logs: []core.ObservabilityLog{}}
		respBytes, _ := json.Marshal(resp)
		assert.Equal(t, 0, CountLogsInObservabilityResponse(string(respBytes)))
	})

	t.Run("invalid json", func(t *testing.T) {
		assert.Equal(t, 0, CountLogsInObservabilityResponse("not json"))
	})

	t.Run("empty string", func(t *testing.T) {
		assert.Equal(t, 0, CountLogsInObservabilityResponse(""))
	})
}

func TestGetErrorLinesFromObservabilityLogString_SeverityPriority(t *testing.T) {
	t.Run("errors prioritized over warnings", func(t *testing.T) {
		// 3 errors + 50 warnings: only errors should appear (since 3 < minErrorLinesForWarningSkip=5, both included)
		var logs []core.ObservabilityLog
		for i := 0; i < 3; i++ {
			logs = append(logs, core.ObservabilityLog{Message: fmt.Sprintf("ERROR: error %d", i)})
		}
		for i := 0; i < 50; i++ {
			logs = append(logs, core.ObservabilityLog{Message: fmt.Sprintf("WARNING: warning %d", i)})
		}
		respObj := core.ObservabilityLogResponse{Logs: logs}
		respBytes, _ := json.Marshal(respObj)

		outStr, err := GetErrorLinesFromObservabilityLogString(string(respBytes))
		assert.NoError(t, err)

		var outObj core.ObservabilityLogResponse
		err = json.Unmarshal([]byte(outStr), &outObj)
		assert.NoError(t, err)

		// With <5 errors, warnings are included too
		assert.Greater(t, len(outObj.Logs), 3)
	})

	t.Run("many errors exclude warnings", func(t *testing.T) {
		// 10 errors + 50 warnings: only errors should appear (10 >= minErrorLinesForWarningSkip=5)
		var logs []core.ObservabilityLog
		for i := 0; i < 10; i++ {
			logs = append(logs, core.ObservabilityLog{Message: fmt.Sprintf("ERROR: error %d", i)})
		}
		for i := 0; i < 50; i++ {
			logs = append(logs, core.ObservabilityLog{Message: fmt.Sprintf("WARNING: warning %d", i)})
		}
		respObj := core.ObservabilityLogResponse{Logs: logs}
		respBytes, _ := json.Marshal(respObj)

		outStr, err := GetErrorLinesFromObservabilityLogString(string(respBytes))
		assert.NoError(t, err)

		var outObj core.ObservabilityLogResponse
		err = json.Unmarshal([]byte(outStr), &outObj)
		assert.NoError(t, err)

		// Only errors should be present (no warning messages)
		for _, log := range outObj.Logs {
			assert.NotContains(t, log.Message, "WARNING:")
		}
	})

	t.Run("only warnings returned when no errors", func(t *testing.T) {
		var logs []core.ObservabilityLog
		for i := 0; i < 5; i++ {
			logs = append(logs, core.ObservabilityLog{Message: fmt.Sprintf("WARNING: warning %d", i)})
		}
		respObj := core.ObservabilityLogResponse{Logs: logs}
		respBytes, _ := json.Marshal(respObj)

		outStr, err := GetErrorLinesFromObservabilityLogString(string(respBytes))
		assert.NoError(t, err)

		var outObj core.ObservabilityLogResponse
		err = json.Unmarshal([]byte(outStr), &outObj)
		assert.NoError(t, err)

		assert.Equal(t, 5, len(outObj.Logs))
	})

	t.Run("all info logs returned as fallback", func(t *testing.T) {
		logs := []core.ObservabilityLog{
			{Message: "INFO: line 1"},
			{Message: "INFO: line 2"},
			{Message: "INFO: line 3"},
		}
		respObj := core.ObservabilityLogResponse{Logs: logs}
		respBytes, _ := json.Marshal(respObj)

		outStr, err := GetErrorLinesFromObservabilityLogString(string(respBytes))
		assert.NoError(t, err)

		var outObj core.ObservabilityLogResponse
		err = json.Unmarshal([]byte(outStr), &outObj)
		assert.NoError(t, err)

		// All logs returned as fallback when no errors or warnings
		assert.Equal(t, 3, len(outObj.Logs))
	})
}

func TestNormalizeLogLine(t *testing.T) {
	assert.Equal(t, "a log line", normalizeLogLine("  a log line  "))
}

func TestGetErrorLinesFromLogStringOrDefault(t *testing.T) {
	log := "INFO: line1\nERROR: line2\nINFO: line3\nWARNING: line4"
	expected := []string{"ERROR: line2", "INFO: line3", "WARNING: line4"}
	assert.Equal(t, expected, GetErrorLinesFromLogStringOrDefault(log, false))
}

func TestGetErrorLinesFromObservabilityLogString(t *testing.T) {
	response := `{"logs":[{"message":"info log"},{"message":"error log"}]}`
	// The new behavior includes "info log" as pre-context for the error log
	expected := `{"logs":[{"labels":null,"message":"info log","severity":"","timestamp":""},{"labels":null,"message":"error log","severity":"","timestamp":""}],"metadata":{"start_time":0,"end_time":0,"limit":0,"query":"","provider":""}}`
	actual, err := GetErrorLinesFromObservabilityLogString(response)
	assert.NoError(t, err)
	assert.JSONEq(t, expected, actual)
}

func TestGetErrorLinesFromObservabilityLogString_Scenarios(t *testing.T) {
	t.Run("ErrorCounting", func(t *testing.T) {
		// Input: 3 identical errors
		logs := []core.ObservabilityLog{
			{Message: "ERROR: Something went wrong"},
			{Message: "ERROR: Something went wrong"},
			{Message: "ERROR: Something went wrong"},
		}
		respObj := core.ObservabilityLogResponse{Logs: logs}
		respBytes, _ := json.Marshal(respObj)

		outStr, err := GetErrorLinesFromObservabilityLogString(string(respBytes))
		assert.NoError(t, err)

		var outObj core.ObservabilityLogResponse
		err = json.Unmarshal([]byte(outStr), &outObj)
		assert.NoError(t, err)

		// Expect only 1 error line (because they are adjacent/identical) with count
		// Since we iterate backwards, the "latest" one is kept.
		// Note: The logic aggregates counts globally first.
		assert.Equal(t, 1, len(outObj.Logs))
		assert.Contains(t, outObj.Logs[0].Message, "(Repeated 3 times)")
	})

	t.Run("ContextOverlap", func(t *testing.T) {
		// Scenario: ERROR A -> Log 1 -> ERROR B
		// Log 1 is Post-Context for A (dist 1) AND Pre-Context for B (dist 1).
		// It should appear exactly once in the correct order.
		logs := []core.ObservabilityLog{
			{Message: "ERROR: A"},
			{Message: "Log 1"},
			{Message: "ERROR: B"},
		}
		respObj := core.ObservabilityLogResponse{Logs: logs}
		respBytes, _ := json.Marshal(respObj)

		outStr, err := GetErrorLinesFromObservabilityLogString(string(respBytes))
		assert.NoError(t, err)

		var outObj core.ObservabilityLogResponse
		err = json.Unmarshal([]byte(outStr), &outObj)
		assert.NoError(t, err)

		// Expect: ERROR A, Log 1, ERROR B
		assert.Equal(t, 3, len(outObj.Logs))
		assert.Equal(t, "ERROR: A", outObj.Logs[0].Message)
		assert.Equal(t, "Log 1", outObj.Logs[1].Message)
		assert.Equal(t, "ERROR: B", outObj.Logs[2].Message)
	})

	t.Run("LimitEnforcement", func(t *testing.T) {
		// Temporarily mock config limit if possible, or just rely on the fact that
		// we pass a huge number of logs.
		// Since config is global, we can't easily change it safely in parallel tests.
		// But we can check if it respects the configured limit (200).

		// Create 300 errors
		var logs []core.ObservabilityLog
		for i := 0; i < 300; i++ {
			logs = append(logs, core.ObservabilityLog{Message: fmt.Sprintf("ERROR: %d", i)})
		}
		respObj := core.ObservabilityLogResponse{Logs: logs}
		respBytes, _ := json.Marshal(respObj)

		outStr, err := GetErrorLinesFromObservabilityLogString(string(respBytes))
		assert.NoError(t, err)

		var outObj core.ObservabilityLogResponse
		err = json.Unmarshal([]byte(outStr), &outObj)
		assert.NoError(t, err)

		// Limit is 200. Output should be <= 200.
		assert.LessOrEqual(t, len(outObj.Logs), 200)
		// It should prioritize the LATEST errors (higher numbers).
		lastLog := outObj.Logs[len(outObj.Logs)-1]
		assert.Equal(t, "ERROR: 299", lastLog.Message)
	})
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isGithubCommandWithJq(tt.command)
			assert.Equal(t, tt.expected, result, "Reason: %s", tt.reason)
		})
	}
}

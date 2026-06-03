package tools

import (
	"encoding/json"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

const testAccountId = "6c008cf8-4d79-4999-8447-573a697d0652"

// CloudWatch rds-deadlock-log-alert event with: json (alert, logs, metrics), prometheus, service_map
// Has 100 error_log_data lines (SQL errors, UUID syntax errors)
const logRichEventId = "121d44fc-c595-4a75-89e9-efcb4c4c9116"

func testUserId() string {
	uid := os.Getenv("TEST_USER")
	if uid == "" {
		uid = uuid.NewString()
	}
	return uid
}

func newToolContext(tool core.NBTool, eventId string) core.NbToolContext {
	sc := security.NewRequestContextForSuperAdmin()
	return core.NewNbToolContext(sc, tool, testAccountId, testUserId(),
		uuid.NewString(), uuid.NewString(), uuid.NewString(),
		eventId, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
}

// TestGetEventEvidence_AllEvidence fetches all evidence and verifies it parses as an event list.
func TestGetEventEvidence_AllEvidence(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id": logRichEventId,
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)
	assert.Equal(t, core.NBToolResponseTypeJson, resp.Type)

	var eventList []events.Event
	err = json.Unmarshal([]byte(resp.Data), &eventList)
	assert.Nil(t, err)
	assert.Len(t, eventList, 1)
	assert.Equal(t, logRichEventId, eventList[0].Id)
}

// TestGetEventEvidence_Logs fetches raw logs evidence.
func TestGetEventEvidence_Logs(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id":      logRichEventId,
			"evidence_type": "logs",
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)

	var logData map[string]any
	err = json.Unmarshal([]byte(resp.Data), &logData)
	assert.Nil(t, err)
	assert.True(t, logData["log_data"] != nil || logData["error_log_data"] != nil,
		"Expected log_data or error_log_data in logs evidence")
}

// TestGetEventEvidence_MetricsData fetches prometheus metrics_data evidence.
func TestGetEventEvidence_MetricsData(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id":      logRichEventId,
			"evidence_type": "metrics_data",
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)
	assert.True(t, json.Valid([]byte(resp.Data)), "Expected valid JSON for metrics_data")
}

// TestGetEventEvidence_NonexistentEvent verifies error handling for valid UUID that doesn't exist.
func TestGetEventEvidence_NonexistentEvent(t *testing.T) {
	tool := GetEventEvidenceTool{}
	fakeId := uuid.NewString()
	ntc := newToolContext(tool, fakeId)

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id": fakeId,
		},
	})
	assert.Nil(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "Event not found")
	assert.Contains(t, resp.Data, "may have expired or been deleted")
}

// TestGetEventEvidence_InvalidUUIDFormat verifies fast-fail on malformed event IDs.
func TestGetEventEvidence_InvalidUUIDFormat(t *testing.T) {
	tests := []struct {
		name    string
		eventId string
	}{
		{"plain string", "not-a-uuid"},
		{"numeric", "12345"},
		{"partial UUID", "6c008cf8-4d79"},
		{"SQL injection attempt", "'; DROP TABLE events; --"},
	}

	tool := GetEventEvidenceTool{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ntc := newToolContext(tool, tc.eventId)
			resp, err := tool.Call(ntc, core.NBToolCallRequest{
				Arguments: map[string]any{
					"event_id": tc.eventId,
				},
			})
			assert.Nil(t, err)
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
			assert.Contains(t, resp.Data, "invalid event_id format")
			assert.Contains(t, resp.Data, "Try searching for recent events")
		})
	}
}

// TestGetEventEvidence_MissingEventId verifies error when event_id is not provided.
func TestGetEventEvidence_MissingEventId(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, "")

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{},
	})
	assert.Nil(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "event_id is required")
}

// TestGetEventEvidence_WhitespaceOnlyEventId verifies whitespace-only IDs are treated as missing.
func TestGetEventEvidence_WhitespaceOnlyEventId(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, "  ")

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id": "  ",
		},
	})
	assert.Nil(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "event_id is required")
}

// TestGetEventEvidence_NonexistentEvidenceType verifies graceful handling when
// requesting an evidence type the event doesn't have.
func TestGetEventEvidence_NonexistentEvidenceType(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	// This event doesn't have traces
	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id":      logRichEventId,
			"evidence_type": "traces",
		},
	})
	assert.Nil(t, err)
	assert.Contains(t, resp.Data, "No 'traces' evidence found")
}

// TestGetEventEvidence_EventIdViaCommand verifies event_id can be passed via Command field.
func TestGetEventEvidence_EventIdViaCommand(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Command: logRichEventId,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)
	assert.Equal(t, core.NBToolResponseTypeJson, resp.Type)
}

// TestEventExecuteTool_SingleEvent tests EventsExecuteTool with the log-rich event.
func TestEventExecuteTool_SingleEvent(t *testing.T) {
	tool := EventsExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	query := `select * from events where id = '` + logRichEventId + `'`
	ntc := core.NewNbToolContext(sc, tool, testAccountId, testUserId(),
		uuid.NewString(), uuid.NewString(), uuid.NewString(),
		query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Command: query,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)

	var eventList []events.Event
	err = json.Unmarshal([]byte(resp.Data), &eventList)
	assert.Nil(t, err)
	assert.Len(t, eventList, 1)

	ev := eventList[0]
	assert.Equal(t, "HIGH", ev.Priority)
	// Should have error logs from the json evidence
	assert.NotEmpty(t, ev.Evidences.ErrorLogData, "Expected ErrorLogData")
	// Should have prometheus metrics
	assert.NotEmpty(t, ev.Evidences.MetricsData, "Expected MetricsData from prometheus evidence")
}

// TestEventExecuteTool_MultiEvent_Manifest tests that querying multiple events (>5)
// produces evidence manifests instead of full evidence data.
func TestEventExecuteTool_MultiEvent_Manifest(t *testing.T) {
	tool := EventsExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	query := `select * from events where priority = 'HIGH' and cloud_account_id = '` + testAccountId + `' order by created_at desc limit 10`
	ntc := core.NewNbToolContext(sc, tool, testAccountId, testUserId(),
		uuid.NewString(), uuid.NewString(), uuid.NewString(),
		query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Command: query,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)

	var rawEvents []map[string]any
	err = json.Unmarshal([]byte(resp.Data), &rawEvents)
	assert.Nil(t, err)

	if len(rawEvents) > 5 {
		// Verify manifest structure in first event
		evidences, ok := rawEvents[0]["evidences"].(map[string]any)
		assert.True(t, ok, "Expected evidences to be a manifest object, not raw evidence")
		assert.Contains(t, evidences, "available_evidence_types")
		assert.Contains(t, evidences, "has_logs")
		assert.Contains(t, evidences, "has_metrics")
		assert.Contains(t, evidences, "has_traces")
		assert.Contains(t, evidences, "has_deployment")
		assert.Contains(t, evidences, "evidence_count")
	} else {
		t.Logf("Only %d events returned, skipping manifest check (need >5)", len(rawEvents))
	}
}

// --- Query mode tests ---

// TestGetEventEvidence_LogsSummary tests query_mode=summary for logs evidence.
// Uses logparser.ExtractPatterns for pattern clustering and GuessLevel for level breakdown.
func TestGetEventEvidence_LogsSummary(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id":      logRichEventId,
			"evidence_type": "logs",
			"query_mode":    "summary",
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)
	t.Logf("Response status: %s, data: %s", resp.Status, resp.Data[:min(len(resp.Data), 500)])
	if resp.Status == core.NBToolResponseStatusError {
		t.Fatalf("Tool returned error: %s", resp.Data)
	}
	assert.Equal(t, core.NBToolResponseTypeJson, resp.Type)

	var summary map[string]any
	err = json.Unmarshal([]byte(resp.Data), &summary)
	assert.Nil(t, err)

	// Must have total_lines and error_lines
	assert.Contains(t, summary, "total_lines")
	assert.Contains(t, summary, "error_lines")
	totalLines, _ := summary["total_lines"].(float64)
	assert.Greater(t, totalLines, float64(0), "Expected non-zero total_lines")

	// top_error_patterns should use logparser Drain3 output format
	patterns, ok := summary["top_error_patterns"].([]any)
	assert.True(t, ok, "Expected top_error_patterns array")
	if !assert.Greater(t, len(patterns), 0, "Expected at least one error pattern") {
		t.FailNow()
	}

	firstPattern, ok := patterns[0].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, firstPattern, "template", "Expected Drain3 template field")
	assert.Contains(t, firstPattern, "count")
	assert.Contains(t, firstPattern, "percentage")
	assert.Contains(t, firstPattern, "example")

	// level_breakdown from logparser.GuessLevel
	if levelBreakdown, ok := summary["level_breakdown"].(map[string]any); ok {
		for _, count := range levelBreakdown {
			c, _ := count.(float64)
			assert.Greater(t, c, float64(0))
		}
	}

	// source field should be "error_log_data" since this event has no log_data
	assert.Equal(t, "error_log_data", summary["source"], "Expected source=error_log_data when log_data is empty")

	t.Logf("Log summary: total_lines=%.0f, error_lines=%.0f, patterns=%d",
		totalLines, summary["error_lines"], len(patterns))
}

// TestGetEventEvidence_LogsFilter tests query_mode=filter for logs evidence.
func TestGetEventEvidence_LogsFilter(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	// Filter for "ERROR" pattern — should match the SQL error lines
	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id":      logRichEventId,
			"evidence_type": "logs",
			"query_mode":    "filter",
			"pattern":       "ERROR",
			"limit":         float64(10),
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)

	var result map[string]any
	err = json.Unmarshal([]byte(resp.Data), &result)
	assert.Nil(t, err)

	assert.Contains(t, result, "lines")
	assert.Contains(t, result, "total_matches")
	assert.Contains(t, result, "total_lines")
	assert.Contains(t, result, "has_more")

	totalMatches, _ := result["total_matches"].(float64)
	assert.Greater(t, totalMatches, float64(0), "Expected matches for 'ERROR' pattern")

	lines := result["lines"].([]any)
	assert.Greater(t, len(lines), 0, "Expected at least one matching line")

	t.Logf("Filter result: total_matches=%.0f, total_lines=%.0f, returned=%d, has_more=%v",
		totalMatches, result["total_lines"], len(lines), result["has_more"])
}

// TestGetEventEvidence_LogsFilterPagination tests offset/limit pagination for filtered logs.
func TestGetEventEvidence_LogsFilterPagination(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	// No pattern = paginate all lines; fetch first 5
	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id":      logRichEventId,
			"evidence_type": "logs",
			"query_mode":    "filter",
			"offset":        float64(0),
			"limit":         float64(5),
		},
	})
	assert.Nil(t, err)

	var page1 map[string]any
	err = json.Unmarshal([]byte(resp.Data), &page1)
	assert.Nil(t, err)

	lines1 := page1["lines"].([]any)
	assert.Equal(t, 5, len(lines1), "Expected exactly 5 lines for limit=5")
	totalLines, _ := page1["total_lines"].(float64)
	assert.Greater(t, totalLines, float64(5), "Expected >5 total lines for pagination test")
	assert.True(t, page1["has_more"].(bool), "Expected has_more=true")

	// Fetch page 2
	resp2, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id":      logRichEventId,
			"evidence_type": "logs",
			"query_mode":    "filter",
			"offset":        float64(5),
			"limit":         float64(5),
		},
	})
	assert.Nil(t, err)

	var page2 map[string]any
	err = json.Unmarshal([]byte(resp2.Data), &page2)
	assert.Nil(t, err)

	lines2 := page2["lines"].([]any)
	assert.Equal(t, 5, len(lines2), "Expected exactly 5 lines for page 2")
	// Pages shouldn't overlap
	assert.NotEqual(t, lines1[0], lines2[0], "Page 2 should have different lines than page 1")

	t.Logf("Pagination: page1=%d lines, page2=%d lines, total=%.0f",
		len(lines1), len(lines2), totalLines)
}

// TestGetEventEvidence_MetricsSummary tests query_mode=summary for prometheus metrics_data.
func TestGetEventEvidence_MetricsSummary(t *testing.T) {
	tool := GetEventEvidenceTool{}
	ntc := newToolContext(tool, logRichEventId)

	resp, err := tool.Call(ntc, core.NBToolCallRequest{
		Arguments: map[string]any{
			"event_id":      logRichEventId,
			"evidence_type": "metrics_data",
			"query_mode":    "summary",
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Data)
	assert.Equal(t, core.NBToolResponseTypeJson, resp.Type)

	var summaries []any
	err = json.Unmarshal([]byte(resp.Data), &summaries)
	assert.Nil(t, err)
	assert.Greater(t, len(summaries), 0, "Expected at least one metric series summary")

	firstSeries, ok := summaries[0].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, firstSeries, "metric")
	assert.Contains(t, firstSeries, "data_points")

	t.Logf("Metrics summary: %d series", len(summaries))
}

// TestFixBareTimestamps verifies that unquoted ISO timestamps in SQL get auto-quoted.
func TestFixBareTimestamps(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"bare timestamp with T separator",
			`SELECT * FROM events WHERE starts_at > 2025-01-25T13:00:00Z`,
			`SELECT * FROM events WHERE starts_at > '2025-01-25T13:00:00Z'`,
		},
		{
			"bare timestamp with timezone offset",
			`SELECT * FROM events WHERE starts_at >= 2026-02-25T13:53:24+05:30`,
			`SELECT * FROM events WHERE starts_at >= '2026-02-25T13:53:24+05:30'`,
		},
		{
			"bare timestamp with fractional seconds",
			`SELECT * FROM events WHERE created_at > 2026-02-25T13:53:24.267112Z`,
			`SELECT * FROM events WHERE created_at > '2026-02-25T13:53:24.267112Z'`,
		},
		{
			"already quoted timestamp unchanged",
			`SELECT * FROM events WHERE starts_at > '2025-01-25T13:00:00Z'`,
			`SELECT * FROM events WHERE starts_at > '2025-01-25T13:00:00Z'`,
		},
		{
			"no timestamp unchanged",
			`SELECT * FROM events WHERE priority = 'HIGH'`,
			`SELECT * FROM events WHERE priority = 'HIGH'`,
		},
		{
			"multiple bare timestamps",
			`SELECT * FROM events WHERE starts_at > 2025-01-25T00:00:00Z AND starts_at < 2025-01-26T00:00:00Z`,
			`SELECT * FROM events WHERE starts_at > '2025-01-25T00:00:00Z' AND starts_at < '2025-01-26T00:00:00Z'`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := fixBareTimestamps(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

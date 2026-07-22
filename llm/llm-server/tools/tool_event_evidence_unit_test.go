package tools

import (
	"encoding/json"
	"math"
	"nudgebee/llm/events"
	core "nudgebee/llm/tools/core"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are hermetic unit tests for the evidence formatting/summarization/filter
// logic. They construct events.InvestigateData fixtures in memory and drive the
// pure functions directly (extractEvidenceByType -> summarize*/filter*), so they
// run in CI without a live DB. The DB-backed Call() path is still covered by the
// integration tests in tool_event_evidence_test.go (gated on TEST_ACCOUNT).

// decodeResponse unmarshals a tool response's JSON Data into the requested type.
func decodeResponse[T any](t *testing.T, data string) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal([]byte(data), &v))
	return v
}

// TestGetEventEvidence_TolerantJSONEnvelopeUnwrap covers the misuse-absorbing
// path added 2026-06-22: 146 of 159 errors on this tool in 30 days had the
// planner passing the entire JSON tool-input envelope as the event_id string
// ({"event_id":"<uuid>","evidence_type":"..."}). The tool now unwraps the
// envelope before UUID validation so the misuse no longer surfaces as an
// error. These tests exercise just the unwrap path — they don't reach the
// DB-backed event lookup, since the UUID-validation error fires synchronously
// and proves whether the unwrap happened.
func TestGetEventEvidence_TolerantJSONEnvelopeUnwrap(t *testing.T) {
	tool := GetEventEvidenceTool{}

	t.Run("unwraps envelope with malformed inner UUID — error names the inner UUID", func(t *testing.T) {
		// If the unwrap works, the UUID-validation error says
		// "invalid event_id format 'not-a-uuid'" (the inner value),
		// NOT "invalid event_id format '{...whole-json...}'".
		resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
			Command: `{"event_id":"not-a-uuid","evidence_type":"all"}`,
		})
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "'not-a-uuid'",
			"unwrap must extract the inner event_id before UUID validation")
		assert.NotContains(t, resp.Data, "event_id\":\"",
			"the unwrapped path must not echo the raw JSON envelope in the error")
	})

	// Note: a positive-path test (valid UUID inside envelope makes it past
	// UUID validation) is intentionally NOT included — the next step in
	// Call() builds a NewNbToolContext that requires a non-nil security
	// context, which the hermetic unit test scaffold can't provide. The
	// malformed-inner-UUID test above is enough to prove the unwrap is
	// reached before validation; positive coverage will arrive via the
	// integration test path in tool_event_evidence_test.go.

	t.Run("bare UUID string passes through unchanged (backwards-compat)", func(t *testing.T) {
		// The dominant pre-change use shape is a bare UUID. Unwrap
		// must be a no-op in that case.
		resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
			Command: "not-a-uuid-either",
		})
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "'not-a-uuid-either'",
			"bare-string path must not be modified by the JSON-envelope unwrap")
	})

	t.Run("malformed JSON envelope falls through to raw UUID validation", func(t *testing.T) {
		// If the string starts with `{` but isn't parseable JSON, we
		// must not crash — fall through to UUID validation against
		// the raw string.
		raw := `{not-json-but-starts-with-brace`
		resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
			Command: raw,
		})
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, raw,
			"malformed JSON must not be silently dropped — the raw input still appears in the validation error")
	})

	t.Run("envelope with missing event_id field falls through to raw validation", func(t *testing.T) {
		// If the JSON parses but has no event_id field, we must not
		// silently set eventId to "" — fall back to validating the raw
		// envelope string (which fails UUID validation cleanly).
		raw := `{"evidence_type":"all","query_mode":"summary"}`
		resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
			Command: raw,
		})
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, raw,
			"envelope without event_id must not be silently accepted as a valid event_id")
	})
}

func TestExtractEvidenceByType(t *testing.T) {
	data := events.InvestigateData{
		LogData:      "line one\nline two",
		ErrorLogData: []string{"ERROR boom"},
		MetricsData:  []events.InvestigateDataInsight{{Type: "prometheus"}},
	}

	t.Run("logs returns both log_data and error_log_data", func(t *testing.T) {
		ev := extractEvidenceByType(data, "logs")
		m, ok := ev.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "line one\nline two", m["log_data"])
		assert.Equal(t, []string{"ERROR boom"}, m["error_log_data"])
	})

	t.Run("metrics_data returns the insight slice", func(t *testing.T) {
		ev := extractEvidenceByType(data, "metrics_data")
		insights, ok := ev.([]events.InvestigateDataInsight)
		require.True(t, ok)
		assert.Len(t, insights, 1)
	})

	t.Run("empty evidence type returns nil", func(t *testing.T) {
		assert.Nil(t, extractEvidenceByType(events.InvestigateData{}, "logs"))
		assert.Nil(t, extractEvidenceByType(events.InvestigateData{}, "traces"))
	})

	t.Run("unknown evidence type returns nil", func(t *testing.T) {
		assert.Nil(t, extractEvidenceByType(data, "not_a_real_type"))
	})
}

func TestSummarizeLogs(t *testing.T) {
	errorLines := []string{
		"ERROR: deadlock detected on table accounts id=1",
		"ERROR: deadlock detected on table accounts id=2",
		"ERROR: deadlock detected on table accounts id=3",
		"ERROR: invalid input syntax for type uuid: \"abc\"",
		"ERROR: invalid input syntax for type uuid: \"def\"",
	}
	data := events.InvestigateData{ErrorLogData: errorLines}
	evidence := extractEvidenceByType(data, "logs")

	tool := GetEventEvidenceTool{}
	resp, err := tool.summarizeLogs(evidence)
	require.NoError(t, err)

	summary := decodeResponse[map[string]any](t, resp.Data)

	// total_lines falls back to error lines when log_data is empty
	assert.Equal(t, float64(len(errorLines)), summary["total_lines"])
	assert.Equal(t, float64(len(errorLines)), summary["error_lines"])
	// source flag set because there is no log_data
	assert.Equal(t, "error_log_data", summary["source"])

	patterns, ok := summary["top_error_patterns"].([]any)
	require.True(t, ok)
	require.Greater(t, len(patterns), 0, "expected Drain3 to cluster the error lines")
	first := patterns[0].(map[string]any)
	assert.Contains(t, first, "template")
	assert.Contains(t, first, "count")
	assert.Contains(t, first, "percentage")
	assert.Contains(t, first, "example")
}

func TestSummarizeLogs_LevelBreakdownFromLogData(t *testing.T) {
	// log_data present => total_lines counts those, level_breakdown derived from them.
	data := events.InvestigateData{
		LogData:      "INFO starting up\nERROR something failed\nWARN heads up",
		ErrorLogData: []string{"ERROR something failed"},
	}
	evidence := extractEvidenceByType(data, "logs")

	tool := GetEventEvidenceTool{}
	resp, err := tool.summarizeLogs(evidence)
	require.NoError(t, err)
	summary := decodeResponse[map[string]any](t, resp.Data)

	assert.Equal(t, float64(3), summary["total_lines"])
	assert.Equal(t, float64(1), summary["error_lines"])
	// source flag only set when log_data is empty
	assert.NotContains(t, summary, "source")
	if lb, ok := summary["level_breakdown"].(map[string]any); ok {
		for _, c := range lb {
			assert.Greater(t, c.(float64), float64(0))
		}
	}
}

func TestFilterLogs_PatternWithContext(t *testing.T) {
	data := events.InvestigateData{
		LogData: "l0\nl1\nERROR target\nl3\nl4\nl5\nl6",
	}
	evidence := extractEvidenceByType(data, "logs")

	tool := GetEventEvidenceTool{}
	resp, err := tool.filterLogs(evidence, "ERROR", 0, 50)
	require.NoError(t, err)
	result := decodeResponse[map[string]any](t, resp.Data)

	assert.Equal(t, float64(1), result["total_matches"])
	assert.Equal(t, float64(7), result["total_lines"])
	assert.Equal(t, false, result["has_more"])

	lines := result["lines"].([]any)
	// match at index 2 plus +/-2 context lines => indices 0..4 => 5 lines
	assert.Len(t, lines, 5)
	assert.Equal(t, "l0", lines[0])
	assert.Equal(t, "ERROR target", lines[2])
	assert.Equal(t, "l4", lines[4])
}

func TestFilterLogs_Pagination(t *testing.T) {
	// 12 lines, no pattern => straight pagination
	var sb []string
	for i := 0; i < 12; i++ {
		sb = append(sb, "line-"+strconv.Itoa(i))
	}
	data := events.InvestigateData{LogData: strings.Join(sb, "\n")}
	evidence := extractEvidenceByType(data, "logs")
	tool := GetEventEvidenceTool{}

	page1, err := tool.filterLogs(evidence, "", 0, 5)
	require.NoError(t, err)
	p1 := decodeResponse[map[string]any](t, page1.Data)
	lines1 := p1["lines"].([]any)
	assert.Len(t, lines1, 5)
	assert.Equal(t, float64(12), p1["total_lines"])
	assert.Equal(t, true, p1["has_more"])
	assert.Equal(t, "line-0", lines1[0])

	page2, err := tool.filterLogs(evidence, "", 5, 5)
	require.NoError(t, err)
	p2 := decodeResponse[map[string]any](t, page2.Data)
	lines2 := p2["lines"].([]any)
	assert.Len(t, lines2, 5)
	assert.Equal(t, true, p2["has_more"])
	assert.Equal(t, "line-5", lines2[0])
	assert.NotEqual(t, lines1[0], lines2[0])

	// Last partial page
	page3, err := tool.filterLogs(evidence, "", 10, 5)
	require.NoError(t, err)
	p3 := decodeResponse[map[string]any](t, page3.Data)
	assert.Len(t, p3["lines"].([]any), 2)
	assert.Equal(t, false, p3["has_more"])
}

func TestSummarizePrometheusMetrics(t *testing.T) {
	insight := events.InvestigateDataInsight{
		Data: map[string]any{
			"result_type": "matrix",
			"series_list_result": []any{
				map[string]any{
					"metric":     map[string]any{"name": "http_requests", "pod": "p1"},
					"values":     []any{1.0, 3.0, 5.0},
					"timestamps": []any{100.0, 200.0, 300.0},
				},
			},
		},
	}
	tool := GetEventEvidenceTool{}
	resp, err := tool.summarizePrometheusMetrics([]events.InvestigateDataInsight{insight})
	require.NoError(t, err)

	summaries := decodeResponse[[]map[string]any](t, resp.Data)
	require.Len(t, summaries, 1)
	s := summaries[0]
	assert.Equal(t, float64(3), s["data_points"])
	assert.Equal(t, float64(1), s["min"])
	assert.Equal(t, float64(5), s["max"])
	assert.Equal(t, float64(3), s["avg"])
	assert.Equal(t, float64(5), s["latest"])
	assert.Equal(t, float64(100), s["first_timestamp"])
	assert.Equal(t, float64(300), s["last_timestamp"])
}

func TestSummarizePodNodeMetrics_MemoryConvertedToMB(t *testing.T) {
	insight := events.InvestigateDataInsight{
		Data: map[string]any{
			"resource_type": "Memory",
			"data": []any{
				map[string]any{
					"metric":     map[string]any{"pod": "p1", "container": "c1"},
					"timestamps": []any{1.0, 2.0},
					"values":     []any{float64(1024 * 1024), float64(2 * 1024 * 1024)},
				},
			},
		},
	}
	tool := GetEventEvidenceTool{}
	resp, err := tool.summarizePodNodeMetrics([]events.InvestigateDataInsight{insight})
	require.NoError(t, err)

	summaries := decodeResponse[[]map[string]any](t, resp.Data)
	require.Len(t, summaries, 1)
	s := summaries[0]
	assert.Equal(t, "MB", s["unit"])
	assert.Equal(t, float64(1), s["min"])
	assert.Equal(t, float64(2), s["max"])
	assert.Equal(t, "p1", s["pod"])
	assert.Equal(t, "c1", s["container"])
	assert.Equal(t, "Memory", s["resource_type"])
}

func TestSummarizeTraces(t *testing.T) {
	insight := events.InvestigateDataInsight{
		Data: map[string]any{
			"data": []any{
				map[string]any{"SpanName": "ok", "ServiceName": "svc-a", "Duration": float64(500_000_000), "StatusCode": "STATUS_CODE_OK"},
				map[string]any{"SpanName": "slow", "ServiceName": "svc-b", "Duration": float64(2_000_000_000), "StatusCode": "STATUS_CODE_OK"},
				map[string]any{"SpanName": "bad", "ServiceName": "svc-a", "Duration": float64(1_500_000_000), "StatusCode": "STATUS_CODE_ERROR"},
			},
		},
	}
	tool := GetEventEvidenceTool{}
	resp, err := tool.summarizeTraces(insight)
	require.NoError(t, err)

	summary := decodeResponse[map[string]any](t, resp.Data)
	assert.Equal(t, float64(3), summary["total_spans"])
	assert.Equal(t, float64(1), summary["error_spans"]) // one STATUS_CODE_ERROR
	assert.Equal(t, float64(2), summary["slow_spans"])  // two spans > 1s
	assert.Len(t, summary["services"].([]any), 2)
	assert.Contains(t, summary, "duration_stats")
	assert.Contains(t, summary, "error_span_details")
	assert.Contains(t, summary, "slow_span_details")
}

func TestPaginateArray(t *testing.T) {
	insights := make([]events.InvestigateDataInsight, 7)
	for i := range insights {
		insights[i] = events.InvestigateDataInsight{Type: strconv.Itoa(i)}
	}
	tool := GetEventEvidenceTool{}
	resp, err := tool.paginateArray(insights, 2, 3)
	require.NoError(t, err)

	result := decodeResponse[map[string]any](t, resp.Data)
	assert.Equal(t, float64(7), result["total"])
	assert.Equal(t, float64(2), result["offset"])
	assert.Equal(t, true, result["has_more"])
	assert.Len(t, result["data"].([]any), 3)
}

func TestFloatHelpers(t *testing.T) {
	vals := []float64{4, 1, 3, 2}
	assert.Equal(t, float64(1), minFloat(vals))
	assert.Equal(t, float64(4), maxFloat(vals))
	assert.Equal(t, float64(2.5), avgFloat(vals))
	assert.Equal(t, float64(0), minFloat(nil))
	assert.Equal(t, float64(0), avgFloat(nil))

	// p95 of 1..100 should land at 95
	ramp := make([]float64, 100)
	for i := range ramp {
		ramp[i] = float64(i + 1)
	}
	assert.Equal(t, float64(95), p95Float(ramp))

	assert.Equal(t, float64(42), toFloat64(42))
	assert.Equal(t, float64(1.5), toFloat64("1.5"))
	assert.True(t, math.IsNaN(toFloat64("not-a-number")))
	assert.True(t, math.IsNaN(toFloat64(struct{}{})))
}

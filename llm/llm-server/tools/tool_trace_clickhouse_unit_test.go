package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	core "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTracesViewExpansion guards the regression where the `traces_view` -> otel_traces
// substitution only matched when the view name was surrounded by literal spaces. Queries
// like `SELECT count(*) FROM traces_view` (no trailing space) were sent verbatim with the
// non-existent `traces_view` table, errored in ClickHouse, and were surfaced as empty.
func TestTracesViewExpansion(t *testing.T) {
	expansion := fmt.Sprintf(tracesViewClickhouseFormat, "otel_traces")

	cases := []string{
		"SELECT count(*) FROM traces_view",                    // no trailing space (the original bug)
		"SELECT * FROM traces_view WHERE workload_name = 'x'", // space-padded
		"SELECT * FROM traces_view\nORDER BY timestamp DESC",  // newline-terminated
		"SELECT * FROM (SELECT * FROM traces_view) t",         // followed by ')'
		"SELECT * FROM default.traces_view LIMIT 5",           // schema-qualified
		"SELECT * FROM TRACES_VIEW",                           // case-insensitive
	}

	for _, q := range cases {
		got := tracesViewPattern.ReplaceAllString(q, " "+expansion+" ")
		assert.NotEqual(t, q, got, "query should have been rewritten: %q", q)
		assert.Contains(t, got, "otel_traces", "expansion should reference otel_traces for: %q", q)
		assert.NotRegexp(t, `(?i)\b(?:default\.)?traces_view\b`, got, "no literal traces_view should remain for: %q", q)
	}
}

// TestTracesViewSchemaColumns guards that the projection exposes exactly the columns the
// traces agent's prompt/examples reference. Missing `endpoint` / `http_status_code` caused
// every agent query that selected them to fail with UNKNOWN_IDENTIFIER.
func TestTracesViewSchemaColumns(t *testing.T) {
	for _, col := range []string{
		"as endpoint",
		"as http_status_code",
		"as resource",
		"as status_code",
		"as workload_name",
		"as duration_ns",
	} {
		assert.True(t, strings.Contains(tracesViewClickhouseFormat, col),
			"traces_view projection must expose column %q (agent prompt references it)", col)
	}
}

// TestSerializeClickhouseTraceResponse_RawTable guards the core fix: when services-server returns
// the raw result table (aggregations, custom projections, scalar counts, span listings), the
// agent-facing payload carries the real columns/values instead of the zeroed span schema that the
// fixed ClickhouseAgentRow mapping produced for aggregation queries.
func TestSerializeClickhouseTraceResponse_RawTable(t *testing.T) {
	queryResponse := core.ObservabilityTraceResponse{
		Result: &core.ObservabilityTraceRawTable{
			Columns:     []string{"service", "request_count", "p95_latency_ns"},
			ColumnTypes: []string{"String", "UInt64", "Float64"},
			Rows: [][]any{
				{"notifications", float64(1200), float64(84213000)},
				{"checkout", float64(950), float64(41020000)},
			},
		},
		Metadata: core.ObservabilityTraceMetadata{Provider: "otel_clickhouse"},
	}

	data, err := serializeClickhouseTraceResponse(queryResponse)
	require.NoError(t, err)

	var got clickhouseRawTraceResponse
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, []string{"service", "request_count", "p95_latency_ns"}, got.Columns,
		"raw table must preserve the real (aggregation) column names and order")
	require.Len(t, got.Rows, 2)
	assert.Equal(t, "notifications", got.Rows[0][0], "first row service value must survive")
	assert.EqualValues(t, 84213000, got.Rows[0][2], "p95 latency value must survive (not zeroed)")

	// The zeroed-span failure mode must NOT appear: no "duration_ns":0 span rows.
	assert.NotContains(t, string(data), `"traces"`,
		"raw-table output must not fall back to the typed span array shape")
}

// TestSerializeClickhouseTraceResponse_LegacyFallback guards rollout version skew: when an older
// services-server returns the typed array (no raw result), the tool still emits the ClickhouseAgentRow
// span shape without error.
func TestSerializeClickhouseTraceResponse_LegacyFallback(t *testing.T) {
	queryResponse := core.ObservabilityTraceResponse{
		Traces: []core.ObservabilityTrace{
			{ServiceName: "notifications", DurationNs: 84213000, SpanName: "GET /notify"},
		},
		Metadata: core.ObservabilityTraceMetadata{Provider: "otel_clickhouse"},
	}

	data, err := serializeClickhouseTraceResponse(queryResponse)
	require.NoError(t, err)

	var got clickhouseTraceResponse
	require.NoError(t, json.Unmarshal(data, &got))

	require.Len(t, got.Traces, 1)
	assert.Equal(t, "notifications", got.Traces[0].ServiceName)
	assert.EqualValues(t, 84213000, got.Traces[0].DurationNs)
}

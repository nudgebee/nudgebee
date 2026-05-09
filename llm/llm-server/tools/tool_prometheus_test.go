package tools

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// sanitizeFloats
// ---------------------------------------------------------------------------

func TestSanitizeFloats_Float64(t *testing.T) {
	assert.Equal(t, float64(0), sanitizeFloats(math.Inf(1)))
	assert.Equal(t, float64(0), sanitizeFloats(math.Inf(-1)))
	assert.Equal(t, float64(0), sanitizeFloats(math.NaN()))
	assert.Equal(t, 3.14, sanitizeFloats(3.14))
	assert.Equal(t, float64(0), sanitizeFloats(float64(0)))
}

func TestSanitizeFloats_Slice(t *testing.T) {
	input := []any{math.Inf(1), 1.0, math.NaN(), "keep"}
	sanitizeFloats(input)
	assert.Equal(t, float64(0), input[0])
	assert.Equal(t, 1.0, input[1])
	assert.Equal(t, float64(0), input[2])
	assert.Equal(t, "keep", input[3])
}

func TestSanitizeFloats_Map(t *testing.T) {
	input := map[string]any{
		"inf":    math.Inf(1),
		"neginf": math.Inf(-1),
		"nan":    math.NaN(),
		"normal": 42.0,
		"str":    "hello",
	}
	sanitizeFloats(input)
	assert.Equal(t, float64(0), input["inf"])
	assert.Equal(t, float64(0), input["neginf"])
	assert.Equal(t, float64(0), input["nan"])
	assert.Equal(t, 42.0, input["normal"])
	assert.Equal(t, "hello", input["str"])
}

func TestSanitizeFloats_Float64Slice(t *testing.T) {
	input := []float64{math.Inf(1), math.NaN(), 5.5, math.Inf(-1), 0}
	sanitizeFloats(input)
	assert.Equal(t, []float64{0, 0, 5.5, 0, 0}, input)
}

func TestSanitizeFloats_NestedStructure(t *testing.T) {
	input := map[string]any{
		"series": []any{
			map[string]any{
				"metric": map[string]any{"name": "cpu"},
				"values": []any{math.Inf(1), 2.5, math.NaN()},
			},
		},
		"scalar": math.Inf(-1),
	}
	sanitizeFloats(input)

	series := input["series"].([]any)
	s0 := series[0].(map[string]any)
	vals := s0["values"].([]any)
	assert.Equal(t, float64(0), vals[0])
	assert.Equal(t, 2.5, vals[1])
	assert.Equal(t, float64(0), vals[2])
	assert.Equal(t, float64(0), input["scalar"])
	// nested map string should be untouched
	assert.Equal(t, "cpu", s0["metric"].(map[string]any)["name"])
}

func TestSanitizeFloats_MarshalableAfter(t *testing.T) {
	// Verify the whole point: JSON marshal succeeds after sanitization
	input := map[string]any{
		"a": math.Inf(1),
		"b": []any{math.NaN(), math.Inf(-1)},
	}
	sanitizeFloats(input)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"a":0`)
}

// ---------------------------------------------------------------------------
// getMappedValuesFromDataList
// ---------------------------------------------------------------------------

func TestGetMappedValuesFromDataList_SeriesListResult(t *testing.T) {
	tool := PrometheusExecuteTool{}
	series := []any{map[string]any{"metric": "cpu", "values": []any{1.0, 2.0}}}
	dataList := []map[string]any{
		{"data": map[string]any{"series_list_result": series}},
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	assert.Equal(t, series, result)
}

func TestGetMappedValuesFromDataList_VectorResult(t *testing.T) {
	tool := PrometheusExecuteTool{}
	vector := []any{map[string]any{"metric": "mem", "value": []any{1234567890.0, "0.5"}}}
	dataList := []map[string]any{
		{"data": map[string]any{"vector_result": vector}},
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	assert.Equal(t, vector, result)
}

func TestGetMappedValuesFromDataList_ScalarResult(t *testing.T) {
	tool := PrometheusExecuteTool{}
	scalar := []any{1234567890.0, "42"}
	dataList := []map[string]any{
		{"data": map[string]any{"scalar_result": scalar}},
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	require.Len(t, result, 1)
	wrapped := result[0].(map[string]any)
	assert.Equal(t, map[string]any{}, wrapped["metric"])
	assert.Equal(t, scalar, wrapped["value"])
}

func TestGetMappedValuesFromDataList_DoubleEncodedJSON(t *testing.T) {
	tool := PrometheusExecuteTool{}
	innerJSON := `{"series_list_result":[{"metric":"cpu","values":[1,2]}]}`
	dataList := []map[string]any{
		{"data": innerJSON},
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	require.Len(t, result, 1)
}

func TestGetMappedValuesFromDataList_QueryWrapper(t *testing.T) {
	tool := PrometheusExecuteTool{}
	series := []any{map[string]any{"metric": "disk"}}
	dataList := []map[string]any{
		{"data": map[string]any{
			"query": map[string]any{
				"series_list_result": series,
			},
		}},
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	assert.Equal(t, series, result)
}

func TestGetMappedValuesFromDataList_ErrorResultType(t *testing.T) {
	tool := PrometheusExecuteTool{}
	dataList := []map[string]any{
		{"data": map[string]any{
			"result_type":   "error",
			"string_result": "invalid PromQL expression",
		}},
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Equal(t, "invalid PromQL expression", err.Error())
}

func TestGetMappedValuesFromDataList_ErrorResultTypeDefaultMsg(t *testing.T) {
	tool := PrometheusExecuteTool{}
	dataList := []map[string]any{
		{"data": map[string]any{"result_type": "error"}},
	}
	_, err := tool.getMappedValuesFromDataList(dataList)
	require.Error(t, err)
	assert.Equal(t, "prometheus query returned an error", err.Error())
}

func TestGetMappedValuesFromDataList_NilAndEmptyEntries(t *testing.T) {
	tool := PrometheusExecuteTool{}

	t.Run("nil data entries skipped", func(t *testing.T) {
		series := []any{map[string]any{"metric": "net"}}
		dataList := []map[string]any{
			nil,
			{"data": nil},
			{"data": map[string]any{"series_list_result": series}},
		}
		result, err := tool.getMappedValuesFromDataList(dataList)
		require.NoError(t, err)
		assert.Equal(t, series, result)
	})

	t.Run("all nil returns empty", func(t *testing.T) {
		dataList := []map[string]any{nil, {"data": nil}}
		result, err := tool.getMappedValuesFromDataList(dataList)
		require.NoError(t, err)
		assert.Equal(t, []any{}, result)
	})

	t.Run("empty dataList returns empty", func(t *testing.T) {
		result, err := tool.getMappedValuesFromDataList([]map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, []any{}, result)
	})
}

func TestGetMappedValuesFromDataList_AllResultFieldsEmpty(t *testing.T) {
	tool := PrometheusExecuteTool{}
	dataList := []map[string]any{
		{"data": map[string]any{
			"series_list_result": []any{},
			"vector_result":      []any{},
			"scalar_result":      []any{},
		}},
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	assert.Equal(t, []any{}, result)
}

func TestGetMappedValuesFromDataList_UnsupportedDataType(t *testing.T) {
	tool := PrometheusExecuteTool{}
	dataList := []map[string]any{
		{"data": 12345}, // int, not map or string
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	assert.Equal(t, []any{}, result)
}

func TestGetMappedValuesFromDataList_InvalidStringJSON(t *testing.T) {
	tool := PrometheusExecuteTool{}
	dataList := []map[string]any{
		{"data": "not valid json"},
	}
	// Invalid JSON string should be skipped (continue), returning empty
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	assert.Equal(t, []any{}, result)
}

func TestGetMappedValuesFromDataList_SeriesTakesPriorityOverVector(t *testing.T) {
	tool := PrometheusExecuteTool{}
	series := []any{map[string]any{"metric": "series_data"}}
	vector := []any{map[string]any{"metric": "vector_data"}}
	dataList := []map[string]any{
		{"data": map[string]any{
			"series_list_result": series,
			"vector_result":      vector,
		}},
	}
	result, err := tool.getMappedValuesFromDataList(dataList)
	require.NoError(t, err)
	assert.Equal(t, series, result)
}

// ---------------------------------------------------------------------------
// getDataFromRelayPrometheusResponse — empty findings
// ---------------------------------------------------------------------------

func TestGetDataFromRelayPrometheusResponse_EmptyFindings(t *testing.T) {
	tool := PrometheusExecuteTool{}
	response := map[string]any{
		"data": map[string]any{
			"findings": []any{},
		},
	}
	result, err := tool.getDataFromRelayPrometheusResponse(response)
	require.NoError(t, err)
	assert.Equal(t, []any{}, result)
}

func TestGetDataFromRelayPrometheusResponse_MissingData(t *testing.T) {
	tool := PrometheusExecuteTool{}
	result, err := tool.getDataFromRelayPrometheusResponse(map[string]any{})
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "data field not found")
}

func TestGetDataFromRelayPrometheusResponse_NilFindings(t *testing.T) {
	tool := PrometheusExecuteTool{}
	response := map[string]any{
		"data": map[string]any{
			"findings": nil,
		},
	}
	result, err := tool.getDataFromRelayPrometheusResponse(response)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "findings field not found")
}

// ---------------------------------------------------------------------------
// extractPromQLFromCommand — the double-serialization guard
// ---------------------------------------------------------------------------

// TestExtractPromQL_ExactErrorPayload reproduces the production 422 error.
// The LLM generated: {"query": "container_memory_working_set_bytes", "range": "1h"}
// as the command string, which Prometheus cannot parse.
func TestExtractPromQL_ExactErrorPayload(t *testing.T) {
	// This is the exact payload that caused the 422 in production.
	command := `{"query": "container_memory_working_set_bytes", "range": "1h"}`
	args := map[string]any{}

	promql, newArgs := extractPromQLFromCommand(command, args)

	assert.Equal(t, "container_memory_working_set_bytes", promql,
		"should extract raw PromQL from JSON-wrapped command")
	assert.Equal(t, "1h", newArgs["range"],
		"should preserve 'range' in arguments")
}

// TestExtractPromQL_WithoutFix_WouldSendJSONToPrometheus shows what happens
// if we skip the guard: the raw JSON becomes the PromQL query.
func TestExtractPromQL_WithoutFix_WouldSendJSONToPrometheus(t *testing.T) {
	command := `{"query": "container_memory_working_set_bytes", "range": "1h"}`

	// Simulate the old code path: no extraction, just backslash/backtick strip.
	queryWithoutFix := command
	queryWithoutFix = strings.ReplaceAll(queryWithoutFix, "\\", "")
	queryWithoutFix = strings.ReplaceAll(queryWithoutFix, "`", "")

	// The unfixed query is still JSON — Prometheus would choke on this.
	assert.True(t, strings.HasPrefix(strings.TrimSpace(queryWithoutFix), "{"),
		"without the fix the query is still a JSON blob")
	assert.Contains(t, queryWithoutFix, `"query"`,
		"without the fix Prometheus sees '\"query\"' instead of a metric name")

	// Now apply the fix.
	promql, _ := extractPromQLFromCommand(command, nil)
	queryWithFix := strings.ReplaceAll(promql, "\\", "")
	queryWithFix = strings.ReplaceAll(queryWithFix, "`", "")

	assert.Equal(t, "container_memory_working_set_bytes", queryWithFix,
		"with the fix Prometheus gets a clean PromQL expression")
}

func TestExtractPromQL_CommandKeyVariant(t *testing.T) {
	// Some LLM outputs use "command" instead of "query" inside the JSON.
	command := `{"command": "rate(http_requests_total[5m])", "start_time": "2024-01-01T00:00:00Z"}`
	args := map[string]any{"end_time": "2024-01-02T00:00:00Z"}

	promql, newArgs := extractPromQLFromCommand(command, args)

	assert.Equal(t, "rate(http_requests_total[5m])", promql)
	assert.Equal(t, "2024-01-01T00:00:00Z", newArgs["start_time"],
		"start_time from JSON should be merged into arguments")
	assert.Equal(t, "2024-01-02T00:00:00Z", newArgs["end_time"],
		"existing args should not be overwritten")
}

func TestExtractPromQL_RawPromQL_Passthrough(t *testing.T) {
	// Normal case: LLM sends a proper PromQL string, not JSON.
	command := "sum(rate(container_cpu_usage_seconds_total[5m])) by (pod)"
	args := map[string]any{"range": "2h"}

	promql, newArgs := extractPromQLFromCommand(command, args)

	assert.Equal(t, command, promql,
		"raw PromQL should be returned unchanged")
	assert.Equal(t, "2h", newArgs["range"],
		"existing arguments should be untouched")
}

func TestExtractPromQL_InvalidJSON_Passthrough(t *testing.T) {
	// Malformed JSON should not panic, just pass through.
	command := `{"query": "container_memory`
	args := map[string]any{}

	promql, newArgs := extractPromQLFromCommand(command, args)

	assert.Equal(t, command, promql,
		"malformed JSON should be returned as-is")
	assert.Empty(t, newArgs)
}

func TestExtractPromQL_NestedQueryObject_NoExtraction(t *testing.T) {
	// If "query" is not a string (e.g. nested object), don't extract.
	command := `{"query": {"metric": "cpu"}, "range": "1h"}`
	args := map[string]any{}

	promql, _ := extractPromQLFromCommand(command, args)

	assert.Equal(t, command, promql,
		"non-string query value should cause passthrough")
}

func TestExtractPromQL_NilArguments(t *testing.T) {
	command := `{"query": "up", "range": "30m"}`

	promql, newArgs := extractPromQLFromCommand(command, nil)

	assert.Equal(t, "up", promql)
	assert.NotNil(t, newArgs, "nil arguments should be initialized")
	assert.Equal(t, "30m", newArgs["range"])
}

func TestExtractPromQL_ExistingArgsNotOverwritten(t *testing.T) {
	command := `{"query": "up", "range": "30m", "start_time": "from-json"}`
	args := map[string]any{"range": "2h"}

	promql, newArgs := extractPromQLFromCommand(command, args)

	assert.Equal(t, "up", promql)
	assert.Equal(t, "2h", newArgs["range"],
		"pre-existing 'range' in args should NOT be overwritten by JSON value")
	assert.Equal(t, "from-json", newArgs["start_time"],
		"new keys from JSON should be added")
}

func TestExtractPromQL_EscapedJSONFromDoubleSerialization(t *testing.T) {
	// Simulates the exact double-serialization: executor_planner marshals
	// a map back to JSON string, then Call() receives it as input.Command.
	inner := map[string]any{
		"query": "container_memory_working_set_bytes",
		"range": "1h",
	}
	commandBytes, err := json.Marshal(inner)
	require.NoError(t, err)
	command := string(commandBytes) // `{"query":"container_memory_working_set_bytes","range":"1h"}`

	promql, newArgs := extractPromQLFromCommand(command, nil)

	assert.Equal(t, "container_memory_working_set_bytes", promql)
	assert.Equal(t, "1h", newArgs["range"])
}

func TestExtractPromQL_SemicolonSeparatedQueries_Passthrough(t *testing.T) {
	// Multiple queries separated by semicolon — not JSON.
	command := "container_cpu_usage_seconds_total;container_memory_working_set_bytes"

	promql, _ := extractPromQLFromCommand(command, nil)

	assert.Equal(t, command, promql,
		"semicolon-separated queries should pass through unchanged")
}

package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"nudgebee/llm/tools/core"
	"strings"
	"sync"
	"testing"
	"time"

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

// TestValidatePromQLSyntax_UnexpectedIdentifierHint pins the tightened hint
// added 2026-07-02. Previously the `unexpected identifier` branch said
// "Check metric name spelling" — misleading because the dominant real cause
// (32 of 62 prometheus errors in the 7d sweep on a single account) was the
// model sending natural-language text ("the CPU utilization", "is memory
// pressure high?", "workloads with restarts"), not a misspelled metric.
// The tightened hint leads with the PromQL-vs-natural-language framing.
func TestValidatePromQLSyntax_UnexpectedIdentifierHint(t *testing.T) {
	// Natural-language input — the case the pre-fix hint mis-steered on.
	got := validatePromQLSyntax("the CPU utilization")
	assert.NotEmpty(t, got, "invalid PromQL must return a structured error")
	assert.Contains(t, got, "PromQL, not natural language",
		"hint must call out the dominant natural-language misuse pattern directly")
	assert.Contains(t, got, "metrics_list",
		"hint must point at metrics_list as the discovery path")
	// The old "check metric name spelling" framing is preserved as a fallback,
	// so real misspellings still get diagnosed.
	assert.Contains(t, got, "check metric name spelling",
		"real-misspelling framing must still surface as the secondary hint")
}

// TestValidatePromQLSyntax_ValidQueryReturnsEmpty locks the contract that a
// syntactically-valid PromQL query produces no error (empty string).
func TestValidatePromQLSyntax_ValidQueryReturnsEmpty(t *testing.T) {
	assert.Empty(t, validatePromQLSyntax(`kube_pod_container_status_restarts_total{namespace="default"}`))
	assert.Empty(t, validatePromQLSyntax(`rate(container_cpu_usage_seconds_total[5m])`))
	assert.Empty(t, validatePromQLSyntax("")) // empty is passthrough, not error
}

// ---------------------------------------------------------------------------
// metrics_label_values
// ---------------------------------------------------------------------------

// label_values() is a Grafana template-variable function, not PromQL. Observed in
// prod: the agent needed a label's values, reached for the Grafana idiom, and got
// back a generic "unknown PromQL function" list that named no way to get them —
// so it guessed another label name instead and the run ended in "no data".
func TestValidatePromQLSyntax_LabelValuesPointsAtLabelValuesTool(t *testing.T) {
	got := validatePromQLSyntax("label_values(node_cpu_seconds_total, instance)")
	require.NotEmpty(t, got, "label_values() is not PromQL and must produce an error")
	assert.Contains(t, got, ToolMetricsLabelValues,
		"hint must name the tool that actually returns label values")
	assert.Contains(t, got, "Grafana",
		"hint must explain why label_values() can never parse here")
	assert.NotContains(t, got, "histogram_quantile()",
		"must not fall through to the generic unknown-function list, which names no recovery path")
}

// The generic unknown-function hint must survive for every other bad function —
// the label_values branch is a narrow carve-out, not a replacement.
func TestValidatePromQLSyntax_OtherUnknownFunctionKeepsGenericHint(t *testing.T) {
	got := validatePromQLSyntax("bogus_function(node_cpu_seconds_total)")
	require.NotEmpty(t, got)
	assert.Contains(t, got, "histogram_quantile()", "generic unknown-function hint must still apply")
	assert.NotContains(t, got, ToolMetricsLabelValues,
		"unrelated unknown functions must not be steered to the label-values tool")
}

func TestFilterAndCapLabelValues_FiltersCaseInsensitivelyAndSorts(t *testing.T) {
	raw := []core.ObservabilityMetricsLabelValue{
		{Value: "10.0.0.2:9100"},
		{Value: "gke-node-b:9100"},
		{Value: ""}, // empty values are dropped, not surfaced as a blank choice
		{Value: "GKE-node-a:9100"},
	}

	values, matched, truncated := filterAndCapLabelValues(raw, "gke-NODE")

	assert.Equal(t, []string{"GKE-node-a:9100", "gke-node-b:9100"}, values)
	assert.Equal(t, 2, matched)
	assert.False(t, truncated)
}

func TestFilterAndCapLabelValues_NoFilterKeepsAllNonEmpty(t *testing.T) {
	raw := []core.ObservabilityMetricsLabelValue{{Value: "b"}, {Value: ""}, {Value: "a"}}

	values, matched, truncated := filterAndCapLabelValues(raw, "")

	assert.Equal(t, []string{"a", "b"}, values)
	assert.Equal(t, 2, matched)
	assert.False(t, truncated)
}

// The pre-cap count is what tells the agent its filter was too broad, so it must
// report the true match count rather than the length of the truncated slice.
func TestFilterAndCapLabelValues_ReportsPreCapCountWhenTruncated(t *testing.T) {
	raw := make([]core.ObservabilityMetricsLabelValue, 0, maxPrometheusLabelValuesInResponse+50)
	for i := 0; i < maxPrometheusLabelValuesInResponse+50; i++ {
		raw = append(raw, core.ObservabilityMetricsLabelValue{Value: fmt.Sprintf("pod-%04d", i)})
	}

	values, matched, truncated := filterAndCapLabelValues(raw, "")

	assert.Len(t, values, maxPrometheusLabelValuesInResponse)
	assert.Equal(t, maxPrometheusLabelValuesInResponse+50, matched, "matched must be the pre-cap total")
	assert.True(t, truncated)
}

func TestFilterAndCapLabelValues_NoMatchesReturnsZero(t *testing.T) {
	raw := []core.ObservabilityMetricsLabelValue{{Value: "10.0.0.2:9100"}}

	values, matched, truncated := filterAndCapLabelValues(raw, "nonexistent")

	assert.Empty(t, values)
	assert.Equal(t, 0, matched)
	assert.False(t, truncated)
}

// A delimited string key would make (filter "a:b", metric "c") and (filter "a",
// metric "b:c") the same cache entry, so one lookup would be served the other's
// values. Colons are not hypothetical here: Prometheus recording-rule metric
// names contain them (cluster:node_cpu:ratio) and the filter is free-form.
func TestLabelValuesCacheKey_ColonsDoNotCollide(t *testing.T) {
	base := labelValuesCacheKey{accountId: "acct", provider: "prometheus", label: "instance"}

	first := base
	first.filter, first.metric = "a:b", "c"
	second := base
	second.filter, second.metric = "a", "b:c"

	assert.NotEqual(t, first, second, "colon placement must remain distinguishable")

	// Struct keys are comparable, so sync.Map treats them as distinct entries.
	var cache sync.Map
	cache.Store(first, "first")
	cache.Store(second, "second")
	got, ok := cache.Load(first)
	require.True(t, ok)
	assert.Equal(t, "first", got, "second Store must not overwrite the first")
}

// Label-value results live in their own cache, so a metrics_list entry can never
// be read back as a label-value entry (or vice versa) even for a coincident key.
func TestLabelValuesCache_IsSeparateFromMetricsListCache(t *testing.T) {
	key := labelValuesCacheKey{accountId: "acct", provider: "prometheus", label: "instance"}
	metricsLabelValuesCache.Store(key, metricsListCacheEntry{
		response: core.NBToolResponse{Data: "label-values"},
		expiry:   time.Now().Add(time.Minute),
	})
	t.Cleanup(func() { metricsLabelValuesCache.Delete(key) })

	_, found := metricsListCache.Load(key)
	assert.False(t, found, "label-value entries must not land in the metrics_list cache")
}

// A missing label must be a self-explanatory error rather than a backend call,
// and it must name the tool that lists valid label names.
func TestListMetricsLabelValuesTool_RequiresLabel(t *testing.T) {
	tool := ListMetricsLabelValuesTool{Provider: "prometheus"}

	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{})

	require.NoError(t, err, "a missing argument is a recoverable LLM mistake, not a tool failure")
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, ToolMetricsLabelsList,
		"error must point at the tool that lists label names")
}

func TestListMetricsLabelValuesTool_SchemaContract(t *testing.T) {
	tool := ListMetricsLabelValuesTool{Provider: "prometheus"}

	assert.Equal(t, ToolMetricsLabelValues, tool.Name())
	schema := tool.InputSchema()
	assert.Equal(t, []string{"label"}, schema.Required)
	for _, key := range []string{"label", "filter", "metric"} {
		assert.Contains(t, schema.Properties, key)
	}
	// metrics_labels_list returns names; this tool is the only source of values.
	// The descriptions must not blur that, or the planner picks the wrong one.
	assert.Contains(t, tool.Description(), "VALUES")
	assert.Contains(t, ListMetricsLabelsTool{}.Description(), "labels")
}

// The empty-result guidance is the agent's only cue at the exact moment it would
// otherwise start guessing label names, so it must route both named-resource
// shapes: workloads to series-match, everything else to label-values.
func TestPrometheusNoDataMessage_RoutesEachDiscoveryShape(t *testing.T) {
	msg := prometheusNoDataMessage(`node_cpu_seconds_total{kubernetes_node="worker-1"}`)

	assert.Contains(t, msg, `node_cpu_seconds_total{kubernetes_node="worker-1"}`,
		"the failing query must be echoed so the agent knows what to change")
	assert.Contains(t, msg, ToolMetricsSeriesMatch, "workload path")
	assert.Contains(t, msg, ToolMetricsLabelValues, "non-workload named-resource path")
	assert.Contains(t, msg, ToolMetricsList, "keyword-discovery path")
	assert.Contains(t, msg, "do NOT guess another label name",
		"must explicitly forbid the observed guess-loop")
}

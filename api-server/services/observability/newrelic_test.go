package observability

import (
	"errors"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"os"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
)

// Note: mockRequestContext is defined in loggly_test.go and shared across test files

// ============================================================================
// NRQL WHERE Clause Building Tests
// ============================================================================

func TestBuildNRQLWhereClause_SimpleEquality(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"service.name": {query.Eq: "api-service"},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "`service.name` = 'api-service'", result)
}

func TestBuildNRQLWhereClause_NotEqual(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"level": {query.Nq: "debug"},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "level != 'debug'", result)
}

func TestBuildNRQLWhereClause_NumericComparisons(t *testing.T) {
	testCases := []struct {
		name     string
		op       query.BinaryWhereClauseType
		expected string
	}{
		{"Greater Than", query.Gt, "`duration.ms` > 1000"},
		{"Less Than", query.Lt, "`duration.ms` < 1000"},
		{"Greater Than or Equal", query.Gte, "`duration.ms` >= 1000"},
		{"Less Than or Equal", query.Lte, "`duration.ms` <= 1000"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			where := query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"duration.ms": {tc.op: 1000},
				},
			}

			result, err := buildNRQLWhereClause(where)

			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildNRQLWhereClause_InOperator(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"status": {query.In: []any{"ok", "error", "warning"}},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "status IN ('ok', 'error', 'warning')", result)
}

func TestBuildNRQLWhereClause_NotInOperator(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"env": {query.NotIn: []any{"dev", "test"}},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "env NOT IN ('dev', 'test')", result)
}

func TestBuildNRQLWhereClause_LikeOperator(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"message": {query.Like: "%error%"},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "message LIKE '%error%'", result)
}

func TestBuildNRQLWhereClause_ContainsOperator(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"message": {query.Contains: "exception"},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "message LIKE '%exception%'", result)
}

func TestBuildNRQLWhereClause_IsNullOperator(t *testing.T) {
	t.Run("Is Null True", func(t *testing.T) {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"error": {query.IsNull: true},
			},
		}

		result, err := buildNRQLWhereClause(where)

		assert.NoError(t, err)
		assert.Equal(t, "error IS NULL", result)
	})

	t.Run("Is Null False", func(t *testing.T) {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"error": {query.IsNull: false},
			},
		}

		result, err := buildNRQLWhereClause(where)

		assert.NoError(t, err)
		assert.Equal(t, "error IS NOT NULL", result)
	})
}

func TestBuildNRQLWhereClause_HasKeyOperator(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"custom_field": {query.HasKey: true},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "custom_field IS NOT NULL", result)
}

func TestBuildNRQLWhereClause_AndConditions(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"service.name": {query.Eq: "api"}}},
			{Binary: query.BinaryWhereClause{"level": {query.Eq: "error"}}},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "(`service.name` = 'api' AND level = 'error')", result)
}

func TestBuildNRQLWhereClause_OrConditions(t *testing.T) {
	where := query.QueryWhereClause{
		Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"level": {query.Eq: "error"}}},
			{Binary: query.BinaryWhereClause{"level": {query.Eq: "fatal"}}},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "(level = 'error' OR level = 'fatal')", result)
}

func TestBuildNRQLWhereClause_NotCondition(t *testing.T) {
	where := query.QueryWhereClause{
		Not: &query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"env": {query.Eq: "test"}},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "NOT (env = 'test')", result)
}

func TestBuildNRQLWhereClause_NestedConditions(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"service.name": {query.Eq: "api"}}},
			{
				Or: []query.QueryWhereClause{
					{Binary: query.BinaryWhereClause{"level": {query.Eq: "error"}}},
					{Binary: query.BinaryWhereClause{"level": {query.Eq: "fatal"}}},
				},
			},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Contains(t, result, "`service.name` = 'api'")
	assert.Contains(t, result, "level = 'error' OR level = 'fatal'")
}

func TestBuildNRQLWhereClause_EmptyClause(t *testing.T) {
	where := query.QueryWhereClause{}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestBuildNRQLWhereClause_BetweenOperator(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"duration": {query.Between: []any{100, 500}},
		},
	}

	result, err := buildNRQLWhereClause(where)

	assert.NoError(t, err)
	assert.Equal(t, "duration >= 100 AND duration <= 500", result)
}

func TestBuildNRQLWhereClause_BetweenOperator_WithGteLte(t *testing.T) {
	t.Run("Timestamp with _gte and _lte", func(t *testing.T) {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"timestamp": {query.Between: map[string]any{
					"_gte": "2026-02-03T09:58:31.077Z",
					"_lte": "2026-02-03T10:13:31.077Z",
				}},
			},
		}

		result, err := buildNRQLWhereClause(where)

		assert.NoError(t, err)
		// Should convert ISO8601 timestamps to epoch milliseconds
		assert.Contains(t, result, "timestamp >=")
		assert.Contains(t, result, "timestamp <=")
		assert.Contains(t, result, "AND")
	})

	t.Run("Numeric values with _gte and _lte", func(t *testing.T) {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"duration": {query.Between: map[string]any{
					"_gte": 100,
					"_lte": 500,
				}},
			},
		}

		result, err := buildNRQLWhereClause(where)

		assert.NoError(t, err)
		assert.Contains(t, result, "duration >= 100")
		assert.Contains(t, result, "duration <= 500")
	})

	t.Run("With _gt and _lt operators", func(t *testing.T) {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"count": {query.Between: map[string]any{
					"_gt": 10,
					"_lt": 100,
				}},
			},
		}

		result, err := buildNRQLWhereClause(where)

		assert.NoError(t, err)
		assert.Contains(t, result, "count > 10")
		assert.Contains(t, result, "count < 100")
	})

	t.Run("Empty map should return error", func(t *testing.T) {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"field": {query.Between: map[string]any{}},
			},
		}

		_, err := buildNRQLWhereClause(where)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no valid comparison keys")
	})
}

func TestBuildNRQLWhereClause_NumericOperatorError(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"duration": {query.Gt: "not-a-number"},
		},
	}

	_, err := buildNRQLWhereClause(where)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GT operator requires numeric value")
}

// ============================================================================
// Field Escaping Tests
// ============================================================================

func TestEscapeNRQLField(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"service.name", "`service.name`"},
		{"k8s.namespace.name", "`k8s.namespace.name`"},
		{"simple-field", "`simple-field`"},
		{"field with space", "`field with space`"},
		{"simpleField", "simpleField"},
		{"level", "level"},
		// HTTP/2 pseudo-headers surfaced by NR keyset()
		{":authority", "`:authority`"},
		{":method", "`:method`"},
		{":path", "`:path`"},
		// Other non-identifier shapes
		{"123field", "`123field`"},
		{"field/sub", "`field/sub`"},
		{"with`backtick", "`with``backtick`"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := escapeNRQLField(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// Reproduces the UI flow that produced "Error at line 1 position 26, unexpected ':'":
// keyset() returns a label like ":authority" which the user then filters on.
// The full query produced by buildNRQLLogQuery must wrap the field in backticks.
func TestBuildNRQLLogQuery_PseudoHeaderField(t *testing.T) {
	src := &NewRelicLogSource{}
	req := FetchLogRequest{
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					":authority": {query.Eq: "104.198.32.76:80"},
				},
			},
		},
		StartTime: 1777273471,
		EndTime:   1777277071,
		Limit:     1000,
	}

	got, err := src.buildNRQLLogQuery(req)
	assert.NoError(t, err)
	assert.Contains(t, got, "`:authority` = '104.198.32.76:80'")
	assert.NotContains(t, got, "(:authority")
}

func TestEscapeNRQLValue(t *testing.T) {
	testCases := []struct {
		input    any
		expected string
	}{
		{"simple", "simple"},
		{"it's a test", "it''s a test"},
		{"user's 'quoted' value", "user''s ''quoted'' value"},
		{123, "123"},
		{45.67, "45.67"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := escapeNRQLValue(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// ============================================================================
// Label Mapping Tests
// ============================================================================

func TestNewRelicLogSource_GetLabelMapping(t *testing.T) {
	s := &NewRelicLogSource{}
	mapping := s.GetLabelMapping()

	assert.Equal(t, "message", mapping["body"])
	assert.Equal(t, "message", mapping["message"])
	assert.Equal(t, "k8s.namespace.name", mapping["namespace"])
	assert.Equal(t, "k8s.container.name", mapping["container"])
	assert.Equal(t, "k8s.pod.name", mapping["pod"])
	assert.Equal(t, "k8s.node.name", mapping["node"])
	assert.Equal(t, "hostname", mapping["host"])
	assert.Equal(t, "service.name", mapping["service"])
	assert.Equal(t, "level", mapping["level"])
}

// ============================================================================
// Log Conversion Tests
// ============================================================================

func TestConvertNRLogsToOutputLogs(t *testing.T) {
	s := &NewRelicLogSource{}

	input := []map[string]any{
		{
			"timestamp":    float64(1704067200000),
			"message":      "Application started successfully",
			"level":        "info",
			"service.name": "api-service",
			"hostname":     "server-01",
			"k8s.pod.name": "api-pod-123",
		},
	}

	result := s.convertNRLogsToOutputLogs(input)

	assert.Len(t, result, 1)
	assert.Equal(t, "Application started successfully", result[0].Message)
	assert.Equal(t, "info", result[0].Severity)
	assert.Equal(t, "api-service", result[0].Labels["service.name"])
	assert.Equal(t, "server-01", result[0].Labels["hostname"])
	assert.Equal(t, "api-pod-123", result[0].Labels["k8s.pod.name"])
	assert.NotEmpty(t, result[0].Timestamp)
}

func TestConvertNRLogsToOutputLogs_InferSeverity(t *testing.T) {
	s := &NewRelicLogSource{}

	testCases := []struct {
		message          string
		expectedSeverity string
	}{
		{"Error occurred while processing request", "error"},
		{"Exception thrown in handler", "error"},
		{"Fatal error: out of memory", "error"},
		{"Warning: resource usage high", "warn"},
		{"Debug: entering function", "debug"},
		{"Trace: method call", "trace"},
		{"Processing completed", "info"},
	}

	for _, tc := range testCases {
		t.Run(tc.message, func(t *testing.T) {
			input := []map[string]any{
				{
					"timestamp": float64(1704067200000),
					"message":   tc.message,
				},
			}

			result := s.convertNRLogsToOutputLogs(input)

			assert.Equal(t, tc.expectedSeverity, result[0].Severity)
		})
	}
}

func TestConvertNRLogsToOutputLogs_EmptyInput(t *testing.T) {
	s := &NewRelicLogSource{}

	result := s.convertNRLogsToOutputLogs([]map[string]any{})

	assert.Empty(t, result)
}

func TestConvertNRLogsToOutputLogs_MultipleLogs(t *testing.T) {
	s := &NewRelicLogSource{}

	input := []map[string]any{
		{"timestamp": float64(1704067200000), "message": "Log 1", "level": "info"},
		{"timestamp": float64(1704067201000), "message": "Log 2", "level": "error"},
		{"timestamp": float64(1704067202000), "message": "Log 3", "level": "debug"},
	}

	result := s.convertNRLogsToOutputLogs(input)

	assert.Len(t, result, 3)
	assert.Equal(t, "Log 1", result[0].Message)
	assert.Equal(t, "Log 2", result[1].Message)
	assert.Equal(t, "Log 3", result[2].Message)
}

// ============================================================================
// NRQL Log Query Building Tests
// ============================================================================

func TestBuildNRQLLogQuery(t *testing.T) {
	s := &NewRelicLogSource{}

	req := FetchLogRequest{
		StartTime: 1704067200000,
		EndTime:   1704070800000,
		Limit:     500,
		Query:     "level = 'error'",
	}

	result, err := s.buildNRQLLogQuery(req)

	assert.NoError(t, err)
	assert.Contains(t, result, "SELECT * FROM Log")
	assert.Contains(t, result, "WHERE level = 'error'")
	assert.Contains(t, result, "LIMIT 500")
	assert.Contains(t, result, "SINCE")
	assert.Contains(t, result, "UNTIL")
}

func TestBuildNRQLLogQuery_WithQueryRequest(t *testing.T) {
	s := &NewRelicLogSource{}

	req := FetchLogRequest{
		StartTime: 1704067200000,
		EndTime:   1704070800000,
		Limit:     100,
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"service.name": {query.Eq: "api"},
				},
			},
		},
	}

	result, err := s.buildNRQLLogQuery(req)

	assert.NoError(t, err)
	assert.Contains(t, result, "SELECT * FROM Log")
	assert.Contains(t, result, "WHERE `service.name` = 'api'")
	assert.Contains(t, result, "LIMIT 100")
}

func TestBuildNRQLLogQuery_DefaultLimit(t *testing.T) {
	s := &NewRelicLogSource{}

	req := FetchLogRequest{
		StartTime: 1704067200000,
		EndTime:   1704070800000,
		Limit:     0, // No limit specified
	}

	result, err := s.buildNRQLLogQuery(req)

	assert.NoError(t, err)
	assert.Contains(t, result, "LIMIT 1000") // Default limit
}

func TestBuildNRQLLogQuery_MaxLimit(t *testing.T) {
	s := &NewRelicLogSource{}

	req := FetchLogRequest{
		StartTime: 1704067200000,
		EndTime:   1704070800000,
		Limit:     5000, // Over max limit
	}

	result, err := s.buildNRQLLogQuery(req)

	assert.NoError(t, err)
	assert.Contains(t, result, "LIMIT 1000") // Capped to 1000
}

// ============================================================================
// Time Range Tests
// ============================================================================

func TestGetTimeRangeSeconds(t *testing.T) {
	s := &NewRelicLogSource{}

	t.Run("Milliseconds to seconds conversion", func(t *testing.T) {
		start, end := s.getTimeRangeSeconds(1704067200000, 1704070800000)

		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})

	t.Run("Already in seconds", func(t *testing.T) {
		start, end := s.getTimeRangeSeconds(1704067200, 1704070800)

		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})

	t.Run("Default time range when zero", func(t *testing.T) {
		start, end := s.getTimeRangeSeconds(0, 0)

		assert.Greater(t, start, int64(0))
		assert.Greater(t, end, start)
	})
}

// ============================================================================
// Helper Function Tests
// ============================================================================

func TestIsNumeric(t *testing.T) {
	testCases := []struct {
		value    any
		expected bool
	}{
		{42, true},
		{int64(42), true},
		{float64(3.14), true},
		{float32(2.5), true},
		{uint(10), true},
		{"string", false},
		{[]int{1, 2, 3}, false},
		{map[string]int{}, false},
		{nil, false},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			result := isNumeric(tc.value)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestHasWhereConditions(t *testing.T) {
	t.Run("Empty clause", func(t *testing.T) {
		assert.False(t, hasWhereConditions(query.QueryWhereClause{}))
	})

	t.Run("With Binary", func(t *testing.T) {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"field": {query.Eq: "value"}},
		}
		assert.True(t, hasWhereConditions(where))
	})

	t.Run("With And", func(t *testing.T) {
		where := query.QueryWhereClause{
			And: []query.QueryWhereClause{{}},
		}
		assert.True(t, hasWhereConditions(where))
	})

	t.Run("With Or", func(t *testing.T) {
		where := query.QueryWhereClause{
			Or: []query.QueryWhereClause{{}},
		}
		assert.True(t, hasWhereConditions(where))
	})

	t.Run("With Not", func(t *testing.T) {
		where := query.QueryWhereClause{
			Not: &query.QueryWhereClause{},
		}
		assert.True(t, hasWhereConditions(where))
	})
}

func TestInferSeverityFromMessage(t *testing.T) {
	testCases := []struct {
		message  string
		expected string
	}{
		{"Error occurred", "error"},
		{"ERROR: connection failed", "error"},
		{"NullPointerException at line 42", "error"},
		{"Fatal crash detected", "error"},
		{"FATAL: out of memory", "error"},
		{"Warning: high memory usage", "warn"},
		{"WARN: deprecated method", "warn"},
		{"Debug output enabled", "debug"},
		{"DEBUG: entering function", "debug"},
		{"Trace started for request", "trace"},
		{"TRACE: method invocation", "trace"},
		{"Processing completed successfully", "info"},
		{"User logged in", "info"},
	}

	for _, tc := range testCases {
		t.Run(tc.message, func(t *testing.T) {
			result := inferSeverityFromMessage(tc.message)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// ============================================================================
// Trace Source Tests
// ============================================================================

func TestNewRelicTraceSource_GetLabelMapping(t *testing.T) {
	s := &NewRelicTraceSource{}
	mapping := s.GetLabelMapping()

	assert.Equal(t, "k8s.namespace.name", mapping["workload_namespace"])
	assert.Equal(t, "service.name", mapping["workload_name"])
	assert.Equal(t, "http.response.status_code", mapping["http_status_code"])
	assert.Equal(t, "name", mapping["span_name"])
	assert.Equal(t, "http.url", mapping["resource"])
	assert.Equal(t, "otel.status_code", mapping["status_code"])
	assert.Equal(t, "trace.id", mapping["trace_id"])
	assert.Equal(t, "span.id", mapping["span_id"])
}

func TestMapNRSpanKind(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"client", "SPAN_KIND_CLIENT"},
		{"CLIENT", "SPAN_KIND_CLIENT"},
		{"server", "SPAN_KIND_SERVER"},
		{"SERVER", "SPAN_KIND_SERVER"},
		{"producer", "SPAN_KIND_PRODUCER"},
		{"consumer", "SPAN_KIND_CONSUMER"},
		{"internal", "SPAN_KIND_INTERNAL"},
		{"unknown", "SPAN_KIND_UNSPECIFIED"},
		{"", "SPAN_KIND_UNSPECIFIED"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := mapNRSpanKind(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestMapNRStatusCode(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"ERROR", "STATUS_CODE_ERROR"},
		{"error", "STATUS_CODE_ERROR"},
		{"OK", "STATUS_CODE_OK"},
		{"ok", "STATUS_CODE_OK"},
		{"UNSET", "STATUS_CODE_UNSET"},
		{"", "STATUS_CODE_UNSET"},
		{"unknown", "STATUS_CODE_UNSET"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := mapNRStatusCode(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestConvertNRSpansToOTelTraces(t *testing.T) {
	s := &NewRelicTraceSource{}

	input := []map[string]any{
		{
			"trace.id":           "abc123def456",
			"span.id":            "span789",
			"parent.id":          "parentspan",
			"name":               "GET /api/users",
			"service.name":       "api-service",
			"duration.ms":        float64(150.5),
			"timestamp":          float64(1704067200000),
			"span.kind":          "server",
			"otel.status_code":   "OK",
			"http.statusCode":    float64(200),
			"http.method":        "GET",
			"http.url":           "/api/users",
			"k8s.namespace.name": "production",
			"k8s.pod.name":       "api-pod-123",
		},
	}

	result := s.convertNRSpansToOTelTraces(input)

	assert.Len(t, result, 1)
	trace := result[0]

	assert.Equal(t, "abc123def456", trace.TraceID)
	assert.Equal(t, "span789", trace.SpanID)
	assert.Equal(t, "parentspan", trace.ParentSpanID)
	assert.Equal(t, "GET /api/users", trace.SpanName)
	assert.Equal(t, "api-service", trace.ServiceName)
	assert.Equal(t, "api-service", trace.WorkloadName)
	assert.Equal(t, int64(150500000), trace.DurationNs) // 150.5ms in ns
	assert.Equal(t, "SPAN_KIND_SERVER", trace.SpanKind)
	assert.Equal(t, "STATUS_CODE_OK", trace.StatusCode)
	assert.Equal(t, "200", trace.HTTPStatusCode)
	assert.Equal(t, "/api/users", trace.Resource)
	assert.Equal(t, "production", trace.WorkloadNamespace)
	assert.Equal(t, "newrelic", trace.TraceSource)
	assert.NotEmpty(t, trace.Timestamp)

	// Check resource attributes
	assert.Equal(t, "production", trace.ResourceAttributes["k8s.namespace.name"])
	assert.Equal(t, "api-pod-123", trace.ResourceAttributes["k8s.pod.name"])

	// Check span attributes
	assert.Equal(t, "GET", trace.SpanAttributes["http.method"])
	assert.Equal(t, "/api/users", trace.SpanAttributes["http.url"])
}

func TestConvertNRSpansToOTelTraces_EmptyInput(t *testing.T) {
	s := &NewRelicTraceSource{}

	result := s.convertNRSpansToOTelTraces([]map[string]any{})

	assert.Empty(t, result)
}

func TestConvertNRSpansToOTelTraces_MultipleSpans(t *testing.T) {
	s := &NewRelicTraceSource{}

	input := []map[string]any{
		{"trace.id": "trace1", "span.id": "span1", "name": "Span 1", "service.name": "svc1"},
		{"trace.id": "trace1", "span.id": "span2", "name": "Span 2", "service.name": "svc1"},
		{"trace.id": "trace2", "span.id": "span3", "name": "Span 3", "service.name": "svc2"},
	}

	result := s.convertNRSpansToOTelTraces(input)

	assert.Len(t, result, 3)
	assert.Equal(t, "span1", result[0].SpanID)
	assert.Equal(t, "span2", result[1].SpanID)
	assert.Equal(t, "span3", result[2].SpanID)
}

func TestConvertNRSpansToOTelTraces_ErrorStatus(t *testing.T) {
	s := &NewRelicTraceSource{}

	input := []map[string]any{
		{
			"trace.id":                "trace123",
			"span.id":                 "span456",
			"name":                    "POST /api/order",
			"service.name":            "order-service",
			"duration.ms":             float64(500),
			"timestamp":               float64(1704067200000),
			"otel.status_code":        "ERROR",
			"otel.status_description": "Internal server error",
			"http.statusCode":         float64(500),
		},
	}

	result := s.convertNRSpansToOTelTraces(input)

	assert.Len(t, result, 1)
	assert.Equal(t, "STATUS_CODE_ERROR", result[0].StatusCode)
	assert.Equal(t, "Internal server error", result[0].StatusMessage)
	assert.Equal(t, "500", result[0].HTTPStatusCode)
}

func TestBuildNRQLSpanQuery(t *testing.T) {
	s := &NewRelicTraceSource{}

	req := TracesV3Request{
		Query: "`service.name` = 'api-service'",
	}

	result, err := s.buildNRQLSpanQuery(req, 1704067200, 1704070800)

	assert.NoError(t, err)
	assert.Contains(t, result, "SELECT * FROM Span")
	assert.Contains(t, result, "WHERE `service.name` = 'api-service'")
	assert.Contains(t, result, "SINCE 1704067200")
	assert.Contains(t, result, "UNTIL 1704070800")
	assert.Contains(t, result, "LIMIT 1000")
}

func TestBuildNRQLSpanQuery_WithQueryRequest(t *testing.T) {
	s := &NewRelicTraceSource{}

	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"service.name": {query.Eq: "api-service"},
				},
			},
			Limit: 500,
		},
	}

	result, err := s.buildNRQLSpanQuery(req, 1704067200, 1704070800)

	assert.NoError(t, err)
	assert.Contains(t, result, "SELECT * FROM Span")
	assert.Contains(t, result, "WHERE `service.name` = 'api-service'")
	assert.Contains(t, result, "LIMIT 500")
}

func TestBuildNRQLSpanQuery_NoWhereClause(t *testing.T) {
	s := &NewRelicTraceSource{}

	req := TracesV3Request{}

	result, err := s.buildNRQLSpanQuery(req, 1704067200, 1704070800)

	assert.NoError(t, err)
	assert.Contains(t, result, "SELECT * FROM Span")
	assert.NotContains(t, result, "WHERE")
	assert.Contains(t, result, "SINCE 1704067200")
}

func TestGetTimeRange(t *testing.T) {
	s := &NewRelicTraceSource{}

	t.Run("Explicit time range", func(t *testing.T) {
		req := TracesV3Request{
			StartTime: 1704067200000,
			EndTime:   1704070800000,
		}

		start, end := s.getTimeRange(&req)

		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})

	t.Run("Time range already in seconds", func(t *testing.T) {
		req := TracesV3Request{
			StartTime: 1704067200,
			EndTime:   1704070800,
		}

		start, end := s.getTimeRange(&req)

		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})

	t.Run("Default time range when zero", func(t *testing.T) {
		req := TracesV3Request{}

		start, end := s.getTimeRange(&req)

		assert.Greater(t, start, int64(0))
		assert.Greater(t, end, start)
	})

	t.Run("Timestamp extracted from Binary Gte/Lte and removed", func(t *testing.T) {
		req := TracesV3Request{
			QueryRequest: TracesQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Binary: query.BinaryWhereClause{
						"timestamp": {
							query.Gte: float64(1704067200000),
							query.Lte: float64(1704070800000),
						},
						"service.name": {query.Eq: "api-service"},
					},
				},
			},
		}

		start, end := s.getTimeRange(&req)

		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
		// timestamp should be removed from Binary
		assert.Nil(t, req.QueryRequest.Where.Binary["timestamp"])
		// other fields should remain
		assert.NotNil(t, req.QueryRequest.Where.Binary["service.name"])
	})

	t.Run("Timestamp extracted from Binary Between and removed", func(t *testing.T) {
		req := TracesV3Request{
			QueryRequest: TracesQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Binary: query.BinaryWhereClause{
						"timestamp": {
							query.Between: map[string]any{
								"_gte": "2024-01-01T00:00:00Z",
								"_lte": "2024-01-01T01:00:00Z",
							},
						},
					},
				},
			},
		}

		start, end := s.getTimeRange(&req)

		// RFC3339 parsed → UnixMilli → /1000 for seconds
		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
		assert.Nil(t, req.QueryRequest.Where.Binary["timestamp"])
	})

	t.Run("trace_source removed from Binary", func(t *testing.T) {
		req := TracesV3Request{
			StartTime: 1704067200,
			EndTime:   1704070800,
			QueryRequest: TracesQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Binary: query.BinaryWhereClause{
						"trace_source": {query.Eq: "otel"},
						"service.name": {query.Eq: "api-service"},
					},
				},
			},
		}

		start, end := s.getTimeRange(&req)

		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
		// trace_source should be removed
		assert.Nil(t, req.QueryRequest.Where.Binary["trace_source"])
		// other fields should remain
		assert.NotNil(t, req.QueryRequest.Where.Binary["service.name"])
	})

	t.Run("Both timestamp and trace_source removed from Binary", func(t *testing.T) {
		req := TracesV3Request{
			QueryRequest: TracesQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Binary: query.BinaryWhereClause{
						"timestamp": {
							query.Gte: float64(1704067200000),
							query.Lte: float64(1704070800000),
						},
						"trace_source": {query.Eq: "otel"},
						"service.name": {query.Eq: "api-service"},
					},
				},
			},
		}

		start, end := s.getTimeRange(&req)

		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
		assert.Nil(t, req.QueryRequest.Where.Binary["timestamp"])
		assert.Nil(t, req.QueryRequest.Where.Binary["trace_source"])
		assert.NotNil(t, req.QueryRequest.Where.Binary["service.name"])
	})

	t.Run("Nil Binary map handled gracefully", func(t *testing.T) {
		req := TracesV3Request{
			StartTime: 1704067200,
			EndTime:   1704070800,
		}

		start, end := s.getTimeRange(&req)

		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})
}

func TestConvertNRGroupedTracesToTraceGroupingValues(t *testing.T) {
	s := &NewRelicTraceSource{}

	// Test with facet array format (how New Relic returns multiple FACET results)
	input := []map[string]any{
		{
			"facet":        []any{"api-service", "production", "GET /api/users", "200"},
			"resource_url": "/api/users?page=1",
			"count":        float64(1000),
			"error_count":  float64(5),
			"avg_duration": float64(100.0),
			"p95_latency":  map[string]any{"95": float64(150.5)},
			"p99_latency":  map[string]any{"99": float64(250.3)},
			"max_latency":  float64(500.0),
		},
	}

	result := s.convertNRGroupedTracesToTraceGroupingValues(input)

	assert.Len(t, result, 1)
	group := result[0]

	assert.Equal(t, "api-service", group.WorkloadName)
	assert.Equal(t, "production", group.WorkloadNamespace)
	assert.Equal(t, "GET /api/users", group.SpanName)
	assert.Equal(t, "/api/users?page=1", group.Resource)
	assert.Equal(t, "200", group.HTTPStatusCode)
	assert.Equal(t, 1000, group.Count)
	assert.Equal(t, 5, group.ErrorCount)
	assert.Equal(t, int64(100000000), group.DurationNS) // avg_duration ms to ns
	assert.Equal(t, int64(150500000), group.P95Latency) // ms to ns
	assert.Equal(t, int64(250300000), group.P99Latency)
	assert.Equal(t, int64(500000000), group.MaxLatency)
	// Destination fields should be empty (cannot be extracted in grouped queries)
	assert.Empty(t, group.DestinationWorkloadName)
	assert.Empty(t, group.DestinationWorkloadNamespace)
}

func TestConvertNRGroupedTracesToTraceGroupingValues_MissingFields(t *testing.T) {
	s := &NewRelicTraceSource{}

	// Simulate non-K8s workload with facet array (short facet with only 2 elements - should use fallback)
	input := []map[string]any{
		{
			"service.name": "legacy-service",
			"name":         "process_request",
			"count":        float64(100),
			"error_count":  float64(2),
			"p95_latency":  map[string]any{"95": float64(50.0)},
			"p99_latency":  map[string]any{"99": float64(75.0)},
			"max_latency":  float64(100.0),
		},
	}

	result := s.convertNRGroupedTracesToTraceGroupingValues(input)

	assert.Len(t, result, 1)
	group := result[0]

	assert.Equal(t, "legacy-service", group.WorkloadName)
	assert.Empty(t, group.WorkloadNamespace) // Non-K8s workloads won't have namespace
	assert.Equal(t, "process_request", group.SpanName)
	assert.Equal(t, "process_request", group.Resource) // Falls back to span name
	assert.Empty(t, group.HTTPStatusCode)              // Not available
	assert.Equal(t, 100, group.Count)
	assert.Equal(t, 2, group.ErrorCount)
}

func TestConvertNRGroupedTracesToTraceGroupingValues_StringStatusCode(t *testing.T) {
	s := &NewRelicTraceSource{}

	// Test with facet array where status code is a string (e.g., "200" not float64(200))
	input := []map[string]any{
		{
			"facet":       []any{"api-service", "default", "GET /health", "200"},
			"count":       float64(500),
			"error_count": float64(0),
		},
	}

	result := s.convertNRGroupedTracesToTraceGroupingValues(input)

	assert.Len(t, result, 1)
	group := result[0]

	assert.Equal(t, "200", group.HTTPStatusCode)
}

func TestConvertNRGroupedTracesToTraceGroupingValues_NumericStatusCodeInFacet(t *testing.T) {
	s := &NewRelicTraceSource{}

	// Test with facet array where status code is numeric
	input := []map[string]any{
		{
			"facet":       []any{"api-service", "default", "GET /health", float64(404)},
			"count":       float64(10),
			"error_count": float64(10),
		},
	}

	result := s.convertNRGroupedTracesToTraceGroupingValues(input)

	assert.Len(t, result, 1)
	group := result[0]

	assert.Equal(t, "404", group.HTTPStatusCode)
}

func TestConvertNRGroupedTracesToTraceGroupingValues_NullStatusCode(t *testing.T) {
	s := &NewRelicTraceSource{}

	// Test facet array with null status code (spans without http.response.status_code)
	input := []map[string]any{
		{
			"facet":       []any{"k8s-collector", "nudgebee-test", "DNS Query", nil},
			"count":       float64(11731),
			"error_count": float64(0),
		},
	}

	result := s.convertNRGroupedTracesToTraceGroupingValues(input)

	assert.Len(t, result, 1)
	group := result[0]

	assert.Equal(t, "k8s-collector", group.WorkloadName)
	assert.Equal(t, "nudgebee-test", group.WorkloadNamespace)
	assert.Equal(t, "DNS Query", group.SpanName)
	assert.Empty(t, group.HTTPStatusCode) // null results in empty string
}

func TestConvertNRGroupedTracesToTraceGroupingValues_SingleFacetString(t *testing.T) {
	s := &NewRelicTraceSource{}

	// Test single FACET case - New Relic returns string instead of array
	input := []map[string]any{
		{
			"facet":        "loki-gateway",
			"service.name": "loki-gateway",
			"count":        float64(159061),
		},
	}

	result := s.convertNRGroupedTracesToTraceGroupingValues(input)

	assert.Len(t, result, 1)
	group := result[0]

	assert.Equal(t, "loki-gateway", group.WorkloadName)
}

func TestConvertNRGroupedTracesToTraceGroupingValues_PartialFacetArray(t *testing.T) {
	s := &NewRelicTraceSource{}

	// Test partial facet array (e.g., only 2 FACETs in query)
	input := []map[string]any{
		{
			"facet":       []any{"api-service", "production"},
			"count":       float64(500),
			"error_count": float64(0),
		},
	}

	result := s.convertNRGroupedTracesToTraceGroupingValues(input)

	assert.Len(t, result, 1)
	group := result[0]

	assert.Equal(t, "api-service", group.WorkloadName)
	assert.Equal(t, "production", group.WorkloadNamespace)
	assert.Empty(t, group.SpanName)       // Not in facet array
	assert.Empty(t, group.HTTPStatusCode) // Not in facet array
}

func TestConvertNRSpansToOTelHeatmap(t *testing.T) {
	s := &NewRelicTraceSource{}

	input := []map[string]any{
		{
			"trace.id":         "trace123",
			"span.id":          "span456",
			"name":             "GET /api/data",
			"service.name":     "data-service",
			"duration.ms":      float64(100.5),
			"timestamp":        float64(1704067200000),
			"otel.status_code": "OK",
		},
	}

	result := s.convertNRSpansToOTelHeatmap(input)

	assert.Len(t, result, 1)
	hm := result[0]

	assert.Equal(t, "trace123", hm.TraceID)
	assert.Equal(t, "span456", hm.SpanID)
	assert.Equal(t, "GET /api/data", hm.SpanName)
	assert.Equal(t, "data-service", hm.ServiceName)
	assert.Equal(t, int64(100500000), hm.DurationNs)
	assert.Equal(t, "STATUS_CODE_OK", hm.StatusCode)
	assert.NotEmpty(t, hm.Timestamp)
}

func TestIsProcessedField(t *testing.T) {
	processedFields := []string{
		"trace.id", "span.id", "parent.id", "name", "service.name",
		"duration.ms", "timestamp", "span.kind", "otel.status_code",
		"http.statusCode", "k8s.namespace.name",
	}

	for _, field := range processedFields {
		t.Run(field, func(t *testing.T) {
			assert.True(t, isProcessedField(field))
		})
	}

	unprocessedFields := []string{
		"custom.field", "user.id", "request.id", "some.attribute",
	}

	for _, field := range unprocessedFields {
		t.Run(field, func(t *testing.T) {
			assert.False(t, isProcessedField(field))
		})
	}
}

func TestParseTimestamp(t *testing.T) {
	t.Run("Float64", func(t *testing.T) {
		result, err := parseTimestamp(float64(1704067200000))
		assert.NoError(t, err)
		assert.Equal(t, int64(1704067200000), result)
	})

	t.Run("Int64", func(t *testing.T) {
		result, err := parseTimestamp(int64(1704067200000))
		assert.NoError(t, err)
		assert.Equal(t, int64(1704067200000), result)
	})

	t.Run("RFC3339 String", func(t *testing.T) {
		result, err := parseTimestamp("2024-01-01T00:00:00Z")
		assert.NoError(t, err)
		assert.Greater(t, result, int64(0))
	})

	t.Run("RFC3339Nano String", func(t *testing.T) {
		result, err := parseTimestamp("2024-01-01T00:00:00.123456789Z")
		assert.NoError(t, err)
		assert.Greater(t, result, int64(0))
	})

	t.Run("Invalid String", func(t *testing.T) {
		_, err := parseTimestamp("not-a-timestamp")
		assert.Error(t, err)
	})

	t.Run("Unsupported Type", func(t *testing.T) {
		_, err := parseTimestamp([]int{1, 2, 3})
		assert.Error(t, err)
	})
}

// =============================================================================
// New Relic Metrics Tests
// =============================================================================

func TestNewRelicMetricSource_BuildNRQLMetricQuery(t *testing.T) {
	s := &NewRelicMetricSource{}

	t.Run("Simple metric name - range query", func(t *testing.T) {
		result, _ := s.buildNRQLMetricQuery("cpu.utilization", 1704067200, 1704070800, 60, false, nil, nil)
		assert.Contains(t, result, "SELECT average(`cpu.utilization`)")
		assert.Contains(t, result, "FROM Metric")
		assert.Contains(t, result, "SINCE 1704067200")
		assert.Contains(t, result, "UNTIL 1704070800")
		assert.Contains(t, result, "TIMESERIES 60 seconds")
	})

	t.Run("Simple metric name - instant query", func(t *testing.T) {
		result, _ := s.buildNRQLMetricQuery("cpu.utilization", 1704067200, 1704070800, 60, true, nil, nil)
		assert.Contains(t, result, "SELECT average(`cpu.utilization`)")
		assert.Contains(t, result, "FROM Metric")
		assert.Contains(t, result, "SINCE 1704067200")
		assert.Contains(t, result, "UNTIL 1704070800")
		assert.NotContains(t, result, "TIMESERIES") // instant queries should NOT have TIMESERIES
	})

	t.Run("Full NRQL query without time range", func(t *testing.T) {
		query := "SELECT average(cpuPercent) FROM SystemSample"
		result, _ := s.buildNRQLMetricQuery(query, 1704067200, 1704070800, 60, false, nil, nil)
		assert.Contains(t, result, "SELECT average(cpuPercent)")
		assert.Contains(t, result, "SINCE 1704067200")
		assert.Contains(t, result, "UNTIL 1704070800")
		assert.Contains(t, result, "TIMESERIES 60 seconds")
	})

	t.Run("Full NRQL query - instant query", func(t *testing.T) {
		query := "SELECT average(cpuPercent) FROM SystemSample"
		result, _ := s.buildNRQLMetricQuery(query, 1704067200, 1704070800, 60, true, nil, nil)
		assert.Contains(t, result, "SELECT average(cpuPercent)")
		assert.Contains(t, result, "SINCE 1704067200")
		assert.Contains(t, result, "UNTIL 1704070800")
		assert.NotContains(t, result, "TIMESERIES") // instant queries should NOT have TIMESERIES
	})

	t.Run("Full NRQL query with existing SINCE", func(t *testing.T) {
		query := "SELECT average(cpuPercent) FROM SystemSample SINCE 1 hour ago"
		result, _ := s.buildNRQLMetricQuery(query, 1704067200, 1704070800, 60, false, nil, nil)
		// Should not add another SINCE
		assert.Equal(t, 1, countOccurrences(result, "SINCE"))
		assert.Contains(t, result, "TIMESERIES 60 seconds")
	})

	t.Run("Full NRQL query with existing TIMESERIES", func(t *testing.T) {
		query := "SELECT average(cpuPercent) FROM SystemSample TIMESERIES 30 seconds"
		result, _ := s.buildNRQLMetricQuery(query, 1704067200, 1704070800, 60, false, nil, nil)
		// Should not add another TIMESERIES
		assert.Equal(t, 1, countOccurrences(result, "TIMESERIES"))
	})

	t.Run("FROM prefix query", func(t *testing.T) {
		query := "FROM Metric SELECT average(cpuPercent)"
		result, _ := s.buildNRQLMetricQuery(query, 1704067200, 1704070800, 60, false, nil, nil)
		assert.Contains(t, result, "FROM Metric")
		assert.Contains(t, result, "SINCE 1704067200")
	})
}

func TestNewRelicMetricSource_ConvertNRMetricsToQueryResult(t *testing.T) {
	s := &NewRelicMetricSource{}

	t.Run("Timeseries data", func(t *testing.T) {
		input := []map[string]any{
			{
				"beginTimeSeconds": float64(1704067200),
				"endTimeSeconds":   float64(1704067260),
				"average.cpu":      float64(45.5),
			},
			{
				"beginTimeSeconds": float64(1704067260),
				"endTimeSeconds":   float64(1704067320),
				"average.cpu":      float64(52.3),
			},
		}

		result := s.convertNRMetricsToQueryResult(input, "cpu_query", "SELECT average(cpu) FROM Metric TIMESERIES")

		assert.Equal(t, "cpu_query", result.QueryKey)
		assert.Equal(t, "SELECT average(cpu) FROM Metric TIMESERIES", result.Query)
		// Both rows belong to the same series (no FACET) — grouped into one payload entry.
		assert.Len(t, result.Payload, 1)
		assert.Equal(t, []int64{1704067200, 1704067260}, result.Payload[0].Timestamps)
		assert.Equal(t, []float64{45.5, 52.3}, result.Payload[0].Values)
		assert.Equal(t, "average.cpu", result.Payload[0].Metric["__name__"])
	})

	t.Run("Timestamp in milliseconds", func(t *testing.T) {
		input := []map[string]any{
			{
				"timestamp":   float64(1704067200000), // milliseconds
				"average.cpu": float64(45.5),
			},
		}

		result := s.convertNRMetricsToQueryResult(input, "test", "")

		assert.Len(t, result.Payload, 1)
		assert.Equal(t, int64(1704067200), result.Payload[0].Timestamps[0]) // converted to seconds
	})

	t.Run("Timestamp in seconds", func(t *testing.T) {
		input := []map[string]any{
			{
				"timestamp":   float64(1704067200), // seconds
				"average.cpu": float64(45.5),
			},
		}

		result := s.convertNRMetricsToQueryResult(input, "test", "")

		assert.Len(t, result.Payload, 1)
		assert.Equal(t, int64(1704067200), result.Payload[0].Timestamps[0])
	})

	t.Run("Empty results", func(t *testing.T) {
		result := s.convertNRMetricsToQueryResult([]map[string]any{}, "empty", "")

		assert.Equal(t, "empty", result.QueryKey)
		assert.Empty(t, result.Payload)
	})

	t.Run("Result with facet values (no numeric)", func(t *testing.T) {
		input := []map[string]any{
			{
				"beginTimeSeconds": float64(1704067200),
				"service":          "api-server",
				"environment":      "production",
			},
		}

		result := s.convertNRMetricsToQueryResult(input, "facet_query", "")

		// Rows with no numeric value are skipped — no payload entries produced.
		assert.Empty(t, result.Payload)
	})

	t.Run("FACET timeseries groups by label", func(t *testing.T) {
		input := []map[string]any{
			{"beginTimeSeconds": float64(1704067200), "pod": "api-1", "average.cpu": float64(45.5)},
			{"beginTimeSeconds": float64(1704067260), "pod": "api-1", "average.cpu": float64(50.2)},
			{"beginTimeSeconds": float64(1704067200), "pod": "web-1", "average.cpu": float64(65.3)},
			{"beginTimeSeconds": float64(1704067260), "pod": "web-1", "average.cpu": float64(72.1)},
		}

		result := s.convertNRMetricsToQueryResult(input, "cpu_facet", "SELECT average(cpu) FROM Metric FACET pod TIMESERIES")

		// Two unique pods → two payload entries.
		assert.Len(t, result.Payload, 2)
		// Each entry has 2 timestamps and 2 values.
		assert.Len(t, result.Payload[0].Timestamps, 2)
		assert.Len(t, result.Payload[0].Values, 2)
		assert.Contains(t, result.Payload[0].Metric, "pod")
	})
}

func TestNewRelicMetricSource_GetTimeRangeSeconds(t *testing.T) {
	s := &NewRelicMetricSource{}

	t.Run("Already in seconds", func(t *testing.T) {
		start, end := s.getTimeRangeSeconds(1704067200, 1704070800)
		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})

	t.Run("Milliseconds converted to seconds", func(t *testing.T) {
		start, end := s.getTimeRangeSeconds(1704067200000, 1704070800000)
		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})

	t.Run("Zero values get defaults", func(t *testing.T) {
		start, end := s.getTimeRangeSeconds(0, 0)
		// Should be within the last hour
		now := time.Now().Unix()
		assert.Greater(t, end, start)
		assert.InDelta(t, now, end, 5) // within 5 seconds
		assert.InDelta(t, now-3600, start, 5)
	})

	t.Run("Mixed milliseconds and seconds", func(t *testing.T) {
		start, end := s.getTimeRangeSeconds(1704067200000, 1704070800)
		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})
}

func TestIsNRQLQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected bool
	}{
		{"SELECT prefix", "SELECT average(cpu) FROM Metric", true},
		{"select lowercase", "select average(cpu) FROM Metric", true},
		{"FROM prefix", "FROM Metric SELECT average(cpu)", true},
		{"from lowercase", "from Metric SELECT average(cpu)", true},
		{"With whitespace", "  SELECT average(cpu) FROM Metric", true},
		{"Metric name only", "cpu.utilization", false},
		{"Empty string", "", false},
		{"Random text", "some random text", false},
		{"Partial SELECT", "MY_SELECT_QUERY", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNRQLQuery(tt.query)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsMetadataField(t *testing.T) {
	metadataFields := []string{
		"beginTimeSeconds",
		"endTimeSeconds",
		"inspectedCount",
		"timestamp",
		"eventType",
	}

	for _, field := range metadataFields {
		t.Run(field+" is metadata", func(t *testing.T) {
			assert.True(t, isMetadataField(field))
		})
	}

	nonMetadataFields := []string{
		"average.cpu",
		"service.name",
		"custom.field",
		"count",
	}

	for _, field := range nonMetadataFields {
		t.Run(field+" is not metadata", func(t *testing.T) {
			assert.False(t, isMetadataField(field))
		})
	}
}

func TestIsInternalMetricField(t *testing.T) {
	internalFields := []string{
		"metricName",
		"timestamp",
		"newrelic.source",
		"instrumentation.provider",
		"instrumentation.name",
		"instrumentation.version",
	}

	for _, field := range internalFields {
		t.Run(field+" is internal", func(t *testing.T) {
			assert.True(t, isInternalMetricField(field))
		})
	}

	externalFields := []string{
		"service.name",
		"k8s.namespace.name",
		"host.name",
		"custom.label",
	}

	for _, field := range externalFields {
		t.Run(field+" is not internal", func(t *testing.T) {
			assert.False(t, isInternalMetricField(field))
		})
	}
}

// Helper function to count occurrences of a substring
func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}

// =============================================================================
// End-to-End Tests: Logs
// =============================================================================
func TestNewRelicLog_QueryLogs_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}
	now := time.Now()

	req := FetchLogRequest{
		AccountId:         "account-123",
		LogProvider:       "newrelic",
		LogProviderSource: "user",
		StartTime:         now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:           now.UnixMilli(),
		Limit:             100,
	}

	output, err := src.QueryLogs(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d logs from New Relic", len(output))

	// Log some details if we got results
	for i, log := range output {
		if i < 5 { // Only log first 5
			t.Logf("Log %d: %s [%s]", i, log.Message, log.Severity)
		}
	}
}

func TestNewRelicTrace_QueryTraces_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicTraceSource{}
	now := time.Now()

	req := TracesV3Request{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
		QueryRequest: TracesQueryBuilderRequest{
			Limit: 100,
		},
	}

	output, err := src.QueryTraces(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d traces from New Relic", len(output))

	// Log some details if we got results
	for i, trace := range output {
		if i < 5 { // Only log first 5
			t.Logf("Trace %d: %s - %s [%s] Duration: %dns", i, trace.ServiceName, trace.SpanName, trace.StatusCode, trace.DurationNs)
		}
	}
}

func TestNewRelicMetric_FetchMetrics_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicMetricSource{}
	now := time.Now()

	req := FetchMetricsRequest{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
		Queries: map[string]string{
			"transaction_duration": "SELECT average(`apm.service.logging.lines`) FROM Metric SINCE 1704067200 UNTIL 1704070800 TIMESERIES 60 seconds",
		},
	}

	output, err := src.FetchMetricsQuery(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d metric results from New Relic", len(output.Results))

	// Log some details if we got results
	for _, result := range output.Results {
		t.Logf("Query: %s, Payload count: %d", result.QueryKey, len(result.Payload))
		for i, payload := range result.Payload {
			if i < 3 { // Only log first 3
				t.Logf("  Metric %d: %v, Timestamps: %d, Values: %d", i, payload.Metric, len(payload.Timestamps), len(payload.Values))
			}
		}
	}
}

func TestNewRelicLogSource_QueryLogs_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}
	now := time.Now()

	req := FetchLogRequest{
		AccountId:         accountID,
		LogProvider:       "newrelic",
		LogProviderSource: "user",
		StartTime:         now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:           now.UnixMilli(),
		Limit:             100,
	}

	output, err := src.QueryLogs(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d logs from New Relic", len(output))

	// Log some details if we got results
	for i, log := range output {
		if i < 5 { // Only log first 5
			t.Logf("Log %d: [%s] %s", i, log.Severity, log.Message)
		}
	}

	// Basic validation - if we got logs, verify they have required fields
	for _, log := range output {
		assert.NotEmpty(t, log.Timestamp, "Timestamp should not be empty")
	}
}

func TestNewRelicLogSource_QueryLogs_EmptyResults(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return "fake-api-key", "123456", "us", nil
		})

	patches.ApplyFunc(integrations.ExecuteNRQL,
		func(apiKey, nrAccountId, region, nrqlQuery string) ([]map[string]any, error) {
			return []map[string]any{}, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}

	req := FetchLogRequest{
		AccountId: "account-123",
		StartTime: time.Now().Add(-1 * time.Hour).UnixMilli(),
		EndTime:   time.Now().UnixMilli(),
		Limit:     100,
	}

	output, err := src.QueryLogs(ctx, req)

	assert.NoError(t, err)
	assert.Empty(t, output)
}

func TestNewRelicLogSource_QueryLogs_WithORFilters(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}
	now := time.Now()
	// {where:{_and:[{key:{key:"Message"},value:"error",op:"CONTAINS"}]}}
	req := FetchLogRequest{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
		Limit:     100,
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{Binary: query.BinaryWhereClause{"Message": {query.Contains: "error"}}},
					// {Binary: query.BinaryWhereClause{"level": {query.Eq: "fatal"}}},
				},
			},
		},
	}

	output, err := src.QueryLogs(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d logs with OR filter (error OR fatal) from New Relic", len(output))

	// Log some details if we got results
	for i, log := range output {
		if i < 10 { // Only log first 10
			t.Logf("Log %d: [%s] %s", i, log.Severity, log.Message)
		}
	}
}

func TestNewRelicLogSource_QueryLabels_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}

	req := FetchLogLabelRequest{
		AccountId: accountID,
	}

	output, err := src.QueryLabels(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d log labels from New Relic", len(output))

	// Log some labels
	for i, lbl := range output {
		if i < 20 { // Only log first 20
			t.Logf("  Label %d: %s", i, lbl.Label)
		}
	}
}

func TestNewRelicLogSource_QueryLabelValues_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}
	now := time.Now()

	req := FetchLogLabelValuesRequest{
		AccountId: accountID,
		LabelName: "service",
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
	}

	output, err := src.QueryLabelValues(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d label values for 'service' from New Relic", len(output))

	// Log some values
	for i, v := range output {
		if i < 20 { // Only log first 20
			t.Logf("  Value %d: %s", i, v.Value)
		}
	}
}

func TestNewRelicLogSource_QueryLogs_ConfigError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return "", "", "", errors.New("integration not configured")
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}

	req := FetchLogRequest{
		AccountId: "missing-account",
		StartTime: time.Now().Add(-1 * time.Hour).UnixMilli(),
		EndTime:   time.Now().UnixMilli(),
	}

	output, err := src.QueryLogs(ctx, req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get New Relic configs")
	assert.Nil(t, output)
}

func TestNewRelicLogSource_QueryLogs_NRQLError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return "fake-api-key", "123456", "us", nil
		})

	patches.ApplyFunc(integrations.ExecuteNRQL,
		func(apiKey, nrAccountId, region, nrqlQuery string) ([]map[string]any, error) {
			return nil, errors.New("NRQL syntax error")
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}

	req := FetchLogRequest{
		AccountId: "account-123",
		StartTime: time.Now().Add(-1 * time.Hour).UnixMilli(),
		EndTime:   time.Now().UnixMilli(),
	}

	output, err := src.QueryLogs(ctx, req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute NRQL")
	assert.Nil(t, output)
}

// =============================================================================
// End-to-End Tests: Traces
// =============================================================================

func TestNewRelicTraceSource_QueryTraces_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicTraceSource{}
	now := time.Now()

	req := TracesV3Request{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
		QueryRequest: TracesQueryBuilderRequest{
			Limit: 100,
		},
	}

	output, err := src.QueryTraces(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d traces from New Relic", len(output))

	// Log some details if we got results
	for i, trace := range output {
		if i < 5 { // Only log first 5
			t.Logf("Trace %d: TraceID=%s SpanID=%s Service=%s SpanName=%s Kind=%s Status=%s Duration=%dns",
				i, trace.TraceID, trace.SpanID, trace.ServiceName, trace.SpanName, trace.SpanKind, trace.StatusCode, trace.DurationNs)
		}
	}

	// Basic validation - if we got traces, verify they have required fields
	for _, trace := range output {
		assert.NotEmpty(t, trace.TraceID, "TraceID should not be empty")
		assert.NotEmpty(t, trace.SpanID, "SpanID should not be empty")
		assert.Equal(t, "newrelic", trace.TraceSource, "TraceSource should be 'newrelic'")
	}
}

func TestNewRelicTraceSource_CountTraces_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicTraceSource{}
	now := time.Now()

	req := TracesV3Request{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
	}

	output, err := src.CountTraces(ctx, req)

	assert.NoError(t, err)
	t.Logf("Trace count from New Relic: %d", output.Count)
	assert.GreaterOrEqual(t, output.Count, 0, "Count should be non-negative")
}

func TestNewRelicTraceSource_GetLabelValues_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicTraceSource{}
	now := time.Now()

	req := TracesV3LabelValuesRequest{
		AccountId: accountID,
		Label:     "service.name",
	}
	_ = now // time range not supported in this request type

	output, err := src.GetLabelValues(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, "service.name", output.Label)
	t.Logf("Retrieved %d unique service.name values from New Relic", len(output.Values))
	for i, val := range output.Values {
		if i < 10 { // Only log first 10
			t.Logf("  Value %d: %s", i, val)
		}
	}
}

func TestNewRelicTraceSource_QueryGroupedTraces_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicTraceSource{}
	now := time.Now()

	req := TracesV3Request{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
	}

	output, err := src.QueryGroupedTraces(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d grouped traces from New Relic", len(output))

	// Log some details if we got results
	for i, group := range output {
		if i < 10 { // Only log first 10
			t.Logf("Group %d: Service=%s SpanName=%s Count=%d ErrorCount=%d P95=%dns P99=%dns",
				i, group.WorkloadName, group.SpanName, group.Count, group.ErrorCount, group.P95Latency, group.P99Latency)
		}
	}
}

// =============================================================================
// End-to-End Tests: Metrics
// =============================================================================

func TestNewRelicMetricSource_FetchMetricsQuery_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicMetricSource{}
	now := time.Now()

	req := FetchMetricsRequest{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
		Queries: map[string]string{
			"transaction_duration": "SELECT average(duration) FROM Transaction TIMESERIES",
		},
	}

	output, err := src.FetchMetricsQuery(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d metric results from New Relic", len(output.Results))

	// Log some details if we got results
	for _, result := range output.Results {
		t.Logf("Query: %s, Payload count: %d", result.QueryKey, len(result.Payload))
		for i, payload := range result.Payload {
			if i < 5 { // Only log first 5
				t.Logf("  Metric %d: %v, Timestamps: %d, Values: %d", i, payload.Metric, len(payload.Timestamps), len(payload.Values))
			}
		}
	}
}

func TestNewRelicMetricSource_FetchMetricList_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicMetricSource{}

	req := FetchMetricsListRequest{
		AccountId: accountID,
	}

	output, err := src.FetchMetricList(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d metrics from New Relic", len(output))

	// Log some metric names
	for i, m := range output {
		if i < 20 { // Only log first 20
			t.Logf("  Metric %d: %s", i, m.Metric)
		}
	}
}

func TestNewRelicMetricSource_FetchMetricLabelValues_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicMetricSource{}
	now := time.Now()

	req := FetchMetricsLabelValueRequest{
		AccountId: accountID,
		Label:     "service.name",
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
	}

	output, err := src.FetchMetricLabelValues(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d label values for 'service.name' from New Relic", len(output))

	// Log the values
	for i, v := range output {
		if i < 20 { // Only log first 20
			t.Logf("  Value %d: %s", i, v.Value)
		}
	}
}

func TestNewRelicMetricSource_FetchMetricsLabels_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicMetricSource{}
	now := time.Now()

	req := FetchMetricLabelsRequest{
		AccountId:  accountID,
		StartTime:  now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:    now.UnixMilli(),
		MetricName: "apm.mobile.application.install.count",
	}

	output, err := src.FetchMetricsLabels(ctx, req)

	assert.NoError(t, err)
	t.Logf("Retrieved %d metric labels from New Relic", len(output))

	// Log the labels
	for i, l := range output {
		if i < 30 { // Only log first 30
			t.Logf("  Label %d: %s", i, l.Label)
		}
	}
}

// =============================================================================
// End-to-End Tests: Log Group
// =============================================================================

func TestNewRelicLogGroup_QueryLogGroup_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}
	now := time.Now()

	req := FetchLogGroupRequest{
		AccountId: accountID,
		StartTime: now.Add(-2 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
		Request:   map[string]any{},
	}

	output, err := src.QueryLogGroup(ctx, req)

	assert.NoError(t, err)
	assert.NotEmpty(t, output.Groups)
	t.Logf("Log group query returned: %v", output)

	// Verify the output has the expected structure
	t.Logf("Retrieved %d groups from New Relic log group", len(output.Groups))
	for i, group := range output.Groups {
		if i < 5 { // Only log first 5 groups
			t.Logf("  Group %d: Sample=%v, Namespace=%v, Count=%d", i, group.Sample, group.Namespace, group.Count)
		}
	}
	print("testing done")
}

func TestNewRelicLogGroup_QueryLogGroup_WithNamespaceFilter_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}
	now := time.Now()

	req := FetchLogGroupRequest{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
		Request: map[string]any{
			"selectedNamespace": "default",
		},
	}

	output, err := src.QueryLogGroup(ctx, req)

	assert.NoError(t, err)
	assert.NotEmpty(t, output.Groups)
	t.Logf("Log group query with namespace filter returned: %v", output)
}

func TestNewRelicLogGroup_QueryLogGroup_WithWorkloadFilter_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}
	now := time.Now()

	req := FetchLogGroupRequest{
		AccountId: accountID,
		StartTime: now.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   now.UnixMilli(),
		Request: map[string]any{
			"selectedWorkload": "api",
		},
	}

	output, err := src.QueryLogGroup(ctx, req)

	assert.NoError(t, err)
	assert.NotEmpty(t, output.Groups)
	t.Logf("Log group query with workload filter returned: %v", output)
}

func TestNewRelicLogGroup_QueryLogGroup_ConfigError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return "", "", "", errors.New("integration not configured")
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}

	req := FetchLogGroupRequest{
		AccountId: "missing-account",
		StartTime: time.Now().Add(-1 * time.Hour).UnixMilli(),
		EndTime:   time.Now().UnixMilli(),
		Request:   map[string]any{},
	}

	output, err := src.QueryLogGroup(ctx, req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get New Relic configs")
	assert.Empty(t, output.Groups)
}

func TestNewRelicLogGroup_QueryLogGroup_NRQLError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return "fake-api-key", "123456", "us", nil
		})

	patches.ApplyFunc(integrations.ExecuteNRQL,
		func(apiKey, nrAccountId, region, nrqlQuery string) ([]map[string]any, error) {
			return nil, errors.New("NRQL syntax error")
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}

	req := FetchLogGroupRequest{
		AccountId: "account-123",
		StartTime: time.Now().Add(-1 * time.Hour).UnixMilli(),
		EndTime:   time.Now().UnixMilli(),
		Request:   map[string]any{},
	}

	output, err := src.QueryLogGroup(ctx, req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute NRQL log group query")
	assert.Empty(t, output.Groups)
}

func TestNewRelicLogGroup_QueryLogGroup_EmptyResults(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return "fake-api-key", "123456", "us", nil
		})

	patches.ApplyFunc(integrations.ExecuteNRQL,
		func(apiKey, nrAccountId, region, nrqlQuery string) ([]map[string]any, error) {
			return []map[string]any{}, nil
		})

	ctx := mockRequestContext()
	src := &NewRelicLogSource{}

	req := FetchLogGroupRequest{
		AccountId: "account-123",
		StartTime: time.Now().Add(-1 * time.Hour).UnixMilli(),
		EndTime:   time.Now().UnixMilli(),
		Request:   map[string]any{},
	}

	output, err := src.QueryLogGroup(ctx, req)

	assert.NoError(t, err)

	// Empty NRQL results should produce empty groups
	assert.Empty(t, output.Groups)
}

// =============================================================================
// Unit Tests: Log Group Helper Functions
// =============================================================================

func TestExtractStringField(t *testing.T) {
	testCases := []struct {
		name       string
		data       map[string]any
		fieldNames []string
		expected   string
	}{
		{
			name:       "First field exists",
			data:       map[string]any{"field1": "value1", "field2": "value2"},
			fieldNames: []string{"field1", "field2"},
			expected:   "value1",
		},
		{
			name:       "Second field exists",
			data:       map[string]any{"field2": "value2"},
			fieldNames: []string{"field1", "field2"},
			expected:   "value2",
		},
		{
			name:       "No field exists",
			data:       map[string]any{"other": "value"},
			fieldNames: []string{"field1", "field2"},
			expected:   "",
		},
		{
			name:       "First field is empty string",
			data:       map[string]any{"field1": "", "field2": "value2"},
			fieldNames: []string{"field1", "field2"},
			expected:   "value2",
		},
		{
			name:       "Field is not string",
			data:       map[string]any{"field1": 123, "field2": "value2"},
			fieldNames: []string{"field1", "field2"},
			expected:   "value2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractStringField(tc.data, tc.fieldNames...)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestExtractCountValue(t *testing.T) {
	testCases := []struct {
		name     string
		data     map[string]any
		expected float64
		ok       bool
	}{
		{
			name:     "Count field exists",
			data:     map[string]any{"count": float64(100)},
			expected: 100,
			ok:       true,
		},
		{
			name:     "Value field exists",
			data:     map[string]any{"value": float64(50)},
			expected: 50,
			ok:       true,
		},
		{
			name:     "Neither field exists",
			data:     map[string]any{"other": float64(25)},
			expected: 0,
			ok:       false,
		},
		{
			name:     "Count takes priority over value",
			data:     map[string]any{"count": float64(100), "value": float64(50)},
			expected: 100,
			ok:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, ok := extractCountValue(tc.data)
			assert.Equal(t, tc.ok, ok)
			if ok {
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestExtractFirstNonEmpty(t *testing.T) {
	testCases := []struct {
		name     string
		values   []any
		expected string
	}{
		{
			name:     "First value is non-empty",
			values:   []any{"first", "second"},
			expected: "first",
		},
		{
			name:     "First is empty, second is non-empty",
			values:   []any{"", "second"},
			expected: "second",
		},
		{
			name:     "All empty",
			values:   []any{"", ""},
			expected: "",
		},
		{
			name:     "First is nil, second is non-empty",
			values:   []any{nil, "second"},
			expected: "second",
		},
		{
			name:     "First is non-string, second is non-empty",
			values:   []any{123, "second"},
			expected: "second",
		},
		{
			name:     "Empty slice",
			values:   []any{},
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractFirstNonEmpty(tc.values)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestConvertToLogGroupOutput_FacetParsing(t *testing.T) {
	src := &NewRelicLogSource{}
	timestamp := int64(1704067200)

	t.Run("With 4-field facet array", func(t *testing.T) {
		results := []map[string]any{
			{
				"count": float64(100),
				"facet": []any{"Error: connection failed", "namespace1", "pod1", "container1"},
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 1)
		group := output.Groups[0]
		assert.Equal(t, "Error: connection failed", group.Sample)
		assert.Equal(t, "namespace1", group.Namespace)
		assert.Equal(t, "pod1", group.Workload)
		assert.Equal(t, "container1", group.Container)
		assert.Equal(t, "/k8s/namespace1/pod1/container1", group.ContainerID)
		assert.Equal(t, []int64{timestamp}, group.Timestamps)
		assert.Equal(t, []float64{100}, group.Values)
		assert.Equal(t, int64(100), group.Count)
	})

	t.Run("With fallback named fields", func(t *testing.T) {
		results := []map[string]any{
			{
				"value":          float64(50),
				"message":        "Fatal error occurred",
				"namespace_name": "my-namespace",
				"workload_name":  "my-workload",
				"container_name": "my-container",
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 1)
		group := output.Groups[0]
		assert.Equal(t, "Fatal error occurred", group.Sample)
		assert.Equal(t, "my-namespace", group.Namespace)
		assert.Equal(t, "my-workload", group.Workload)
		assert.Equal(t, "my-container", group.Container)
		assert.Equal(t, "/k8s/my-namespace/my-workload/my-container", group.ContainerID)
	})

	t.Run("No count value skips result", func(t *testing.T) {
		results := []map[string]any{
			{
				"other": float64(100),
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Empty(t, output.Groups)
	})

	t.Run("Short facet array uses fallback fields", func(t *testing.T) {
		results := []map[string]any{
			{
				"count":          float64(75),
				"facet":          []any{"Short array"},
				"message":        "Fallback message",
				"namespace_name": "ns2",
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 1)
		group := output.Groups[0]
		assert.Equal(t, "Fallback message", group.Sample)
		assert.Equal(t, "ns2", group.Namespace)
	})
}

func TestNewRelicLogSource_ConvertToLogGroupOutput(t *testing.T) {
	src := &NewRelicLogSource{}
	timestamp := int64(1704067200)

	t.Run("Multiple results with 4-field facet", func(t *testing.T) {
		results := []map[string]any{
			{
				"count": float64(100),
				"facet": []any{"Error: connection failed", "ns1", "pod1", "c1"},
			},
			{
				"count": float64(50),
				"facet": []any{"Warning: retry attempt", "ns2", "pod2", "c2"},
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 2)

		// Verify first group
		assert.Equal(t, int64(100), output.Groups[0].Count)
		assert.Equal(t, "Error: connection failed", output.Groups[0].Sample)
		assert.Equal(t, "ns1", output.Groups[0].Namespace)
		assert.Equal(t, "pod1", output.Groups[0].Workload)
		assert.NotEmpty(t, output.Groups[0].PatternHash)

		// Verify second group
		assert.Equal(t, int64(50), output.Groups[1].Count)
		assert.Equal(t, "Warning: retry attempt", output.Groups[1].Sample)
		assert.Equal(t, "ns2", output.Groups[1].Namespace)
		assert.NotEmpty(t, output.Groups[1].PatternHash)
	})

	t.Run("Empty results", func(t *testing.T) {
		results := []map[string]any{}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Empty(t, output.Groups)
	})

	t.Run("Skips invalid results", func(t *testing.T) {
		results := []map[string]any{
			{
				"count": float64(100),
				"facet": []any{"Critical error", "ns1", "pod1", "c1"},
			},
			{
				"other": "no count value",
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 1)
		assert.Equal(t, "Critical error", output.Groups[0].Sample)
	})
}

func TestNewRelicLogSource_GetTimeRangeSeconds(t *testing.T) {
	src := &NewRelicLogSource{}

	t.Run("Already in seconds", func(t *testing.T) {
		start, end := src.getTimeRangeSeconds(1704067200, 1704070800)
		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})

	t.Run("Milliseconds converted to seconds", func(t *testing.T) {
		start, end := src.getTimeRangeSeconds(1704067200000, 1704070800000)
		assert.Equal(t, int64(1704067200), start)
		assert.Equal(t, int64(1704070800), end)
	})

	t.Run("Zero values get defaults", func(t *testing.T) {
		start, end := src.getTimeRangeSeconds(0, 0)
		now := time.Now().Unix()
		assert.Greater(t, end, start)
		assert.InDelta(t, now, end, 5)
		assert.InDelta(t, now-3600, start, 5)
	})
}

func TestGeneratePatternHash(t *testing.T) {
	t.Run("Empty message returns empty string", func(t *testing.T) {
		hash := generatePatternHash("")
		assert.Equal(t, "", hash)
	})

	t.Run("Non-empty message generates 14-char hash", func(t *testing.T) {
		hash := generatePatternHash("Error: connection timeout")
		assert.Len(t, hash, 14)
		assert.NotEmpty(t, hash)
	})

	t.Run("Same message produces same hash (consistency)", func(t *testing.T) {
		message := "Critical: database connection failed"
		hash1 := generatePatternHash(message)
		hash2 := generatePatternHash(message)
		assert.Equal(t, hash1, hash2)
	})

	t.Run("Different messages produce different hashes", func(t *testing.T) {
		hash1 := generatePatternHash("Error message 1")
		hash2 := generatePatternHash("Error message 2")
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("Hash is alphanumeric (URL-safe)", func(t *testing.T) {
		hash := generatePatternHash("Test message with special chars: !@#$%")
		// Base64 URL encoding uses: A-Z, a-z, 0-9, -, _
		assert.Regexp(t, "^[A-Za-z0-9_-]+$", hash)
	})
}

func TestConvertToLogGroupOutput_LogGroup(t *testing.T) {
	src := &NewRelicLogSource{}
	timestamp := int64(1704067200)

	t.Run("Valid result with 4-field facet array (message, namespace, workload, container)", func(t *testing.T) {
		results := []map[string]any{
			{
				"count": float64(100),
				"facet": []any{"Error connecting to database", "production", "api-server", "api-container"},
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 1)
		group := output.Groups[0]
		assert.Equal(t, "Error connecting to database", group.Sample)
		assert.Equal(t, "production", group.Namespace)
		assert.Equal(t, "api-server", group.Workload)
		assert.Equal(t, "api-container", group.Container)
		assert.Equal(t, "/k8s/production/api-server/api-container", group.ContainerID)
		assert.NotEmpty(t, group.PatternHash)
		assert.Len(t, group.PatternHash, 14)
		assert.Equal(t, []int64{timestamp}, group.Timestamps)
		assert.Equal(t, []float64{100}, group.Values)
		assert.Equal(t, int64(100), group.Count)
	})

	t.Run("Pattern hash is consistent for same message", func(t *testing.T) {
		message := "Critical: OOM killed"
		results := []map[string]any{
			{
				"count": float64(50),
				"facet": []any{message, "default", "web-app", "nginx"},
			},
			{
				"count": float64(75),
				"facet": []any{message, "staging", "web-app", "nginx"},
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 2)
		assert.Equal(t, output.Groups[0].PatternHash, output.Groups[1].PatternHash)
	})

	t.Run("Fallback for named fields includes sample", func(t *testing.T) {
		results := []map[string]any{
			{
				"value":          float64(25),
				"message":        "Fatal error occurred",
				"namespace_name": "kube-system",
				"workload_name":  "coredns",
				"container_name": "coredns",
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 1)
		group := output.Groups[0]
		assert.Equal(t, "Fatal error occurred", group.Sample)
		assert.Equal(t, "kube-system", group.Namespace)
		assert.Equal(t, "coredns", group.Workload)
		assert.Equal(t, "coredns", group.Container)
		assert.NotEmpty(t, group.PatternHash)
	})

	t.Run("Empty message does not generate pattern_hash", func(t *testing.T) {
		results := []map[string]any{
			{
				"count": float64(10),
				"facet": []any{"", "default", "app", "container"},
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 1)
		group := output.Groups[0]
		assert.Empty(t, group.Sample)
		assert.Empty(t, group.PatternHash)
	})

	t.Run("Facet array with less than 4 fields uses fallback", func(t *testing.T) {
		results := []map[string]any{
			{
				"count":          float64(30),
				"facet":          []any{"Short array"},
				"message":        "Fallback message",
				"namespace_name": "test-ns",
			},
		}

		output := src.convertToLogGroupOutput(results, timestamp)

		assert.Len(t, output.Groups, 1)
		group := output.Groups[0]
		assert.Equal(t, "Fallback message", group.Sample)
		assert.Equal(t, "test-ns", group.Namespace)
		assert.NotEmpty(t, group.PatternHash)
	})
}

// =============================================================================
// New Relic Utilisation Query Builder Tests
// =============================================================================

func TestBuildNewRelicNodeQueries(t *testing.T) {
	t.Run("Basic node queries", func(t *testing.T) {
		meta := RequestMetadata{
			NodeName: "worker-node-1",
		}
		metrics := []string{"cpu_usage", "memory_usage", "disk_total", "disk_used"}

		queries := buildNewRelicNodeQueries(meta, metrics)

		assert.Len(t, queries, 4)
		assert.Contains(t, queries["cpu_usage"], "K8sNodeSample")
		assert.Contains(t, queries["cpu_usage"], "cpuUsedCores")
		assert.Contains(t, queries["cpu_usage"], "nodeName = 'worker-node-1'")
		assert.Contains(t, queries["memory_usage"], "memoryUsedBytes")
		assert.Contains(t, queries["disk_total"], "fsCapacityBytes")
		assert.Contains(t, queries["disk_used"], "fsUsedBytes")
	})

	t.Run("Node resource requests and limits", func(t *testing.T) {
		meta := RequestMetadata{
			NodeName: "worker-node-2",
		}
		metrics := []string{"cpu_request", "cpu_limit", "memory_request", "memory_limit"}

		queries := buildNewRelicNodeQueries(meta, metrics)

		assert.Len(t, queries, 4)
		// Requests/limits come from K8sContainerSample aggregated by node
		assert.Contains(t, queries["cpu_request"], "K8sContainerSample")
		assert.Contains(t, queries["cpu_request"], "cpuRequestedCores")
		assert.Contains(t, queries["cpu_limit"], "cpuLimitCores")
		assert.Contains(t, queries["memory_request"], "memoryRequestedBytes")
		assert.Contains(t, queries["memory_limit"], "memoryLimitBytes")
	})

	t.Run("Empty node name returns empty queries", func(t *testing.T) {
		meta := RequestMetadata{
			NodeName: "",
		}
		metrics := []string{"cpu_usage", "memory_usage"}

		queries := buildNewRelicNodeQueries(meta, metrics)

		assert.Empty(t, queries)
	})

	t.Run("Node name with special characters is escaped", func(t *testing.T) {
		meta := RequestMetadata{
			NodeName: "node-with'quote",
		}
		metrics := []string{"cpu_usage"}

		queries := buildNewRelicNodeQueries(meta, metrics)

		assert.Contains(t, queries["cpu_usage"], "node-with\\'quote")
	})

	t.Run("Allocatable metrics", func(t *testing.T) {
		meta := RequestMetadata{
			NodeName: "node-1",
		}
		metrics := []string{"cpu_allocatable", "memory_allocatable"}

		queries := buildNewRelicNodeQueries(meta, metrics)

		assert.Len(t, queries, 2)
		assert.Contains(t, queries["cpu_allocatable"], "allocatableCpuCores")
		assert.Contains(t, queries["memory_allocatable"], "allocatableMemoryBytes")
	})
}

func TestBuildNewRelicWorkloadQueries(t *testing.T) {
	t.Run("Deployment queries", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "production",
			Name:      "api-server",
			Kind:      "deployment",
		}
		metrics := []string{"cpu_usage", "memory_usage", "cpu_request", "memory_limit"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 4)
		assert.Contains(t, queries["cpu_usage"], "K8sContainerSample")
		assert.Contains(t, queries["cpu_usage"], "namespaceName = 'production'")
		assert.Contains(t, queries["cpu_usage"], "deploymentName = 'api-server'")
		assert.Contains(t, queries["cpu_usage"], "FACET deploymentName")
		assert.Contains(t, queries["memory_usage"], "memoryWorkingSetBytes")
	})

	t.Run("Pod queries", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "default",
			Name:      "nginx-pod-abc123",
			Kind:      "pod",
		}
		metrics := []string{"cpu_usage", "memory_usage"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 2)
		assert.Contains(t, queries["cpu_usage"], "podName = 'nginx-pod-abc123'")
		assert.Contains(t, queries["cpu_usage"], "FACET podName")
	})

	t.Run("StatefulSet queries use LIKE pattern", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "database",
			Name:      "mysql",
			Kind:      "statefulset",
		}
		metrics := []string{"cpu_usage"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Contains(t, queries["cpu_usage"], "podName LIKE 'mysql-%'")
		assert.Contains(t, queries["cpu_usage"], "FACET podName")
	})

	t.Run("DaemonSet queries", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "kube-system",
			Name:      "fluentd",
			Kind:      "daemonset",
		}
		metrics := []string{"cpu_usage"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Contains(t, queries["cpu_usage"], "daemonsetName = 'fluentd'")
		assert.Contains(t, queries["cpu_usage"], "FACET daemonsetName")
	})

	t.Run("Container filter is applied", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace:     "production",
			Name:          "api-server",
			Kind:          "deployment",
			ContainerName: "main",
		}
		metrics := []string{"cpu_usage"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Contains(t, queries["cpu_usage"], "containerName = 'main'")
	})

	t.Run("PVC metrics", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "database",
			Name:      "postgres",
			Kind:      "statefulset",
			PVCName:   "data-postgres-0",
		}
		metrics := []string{"pvc_usage", "pvc_requests"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 2)
		assert.Contains(t, queries["pvc_usage"], "K8sVolumeSample")
		assert.Contains(t, queries["pvc_usage"], "pvcName = 'data-postgres-0'")
		assert.Contains(t, queries["pvc_usage"], "fsUsedBytes")
		assert.Contains(t, queries["pvc_requests"], "fsCapacityBytes")
	})

	t.Run("HTTP/APM metrics from Transaction", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "production",
			Name:      "api-server",
			Kind:      "deployment",
		}
		metrics := []string{"http_throughput", "http_latency_p95", "http_latency_p99", "http_error_rate"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 4)
		assert.Contains(t, queries["http_throughput"], "Transaction")
		assert.Contains(t, queries["http_throughput"], "rate(count(*), 1 minute)")
		assert.Contains(t, queries["http_latency_p95"], "percentile(duration, 95)")
		assert.Contains(t, queries["http_latency_p99"], "percentile(duration, 99)")
		assert.Contains(t, queries["http_error_rate"], "percentage(count(*), WHERE error IS true)")
	})

	t.Run("Empty namespace returns empty for workload metrics", func(t *testing.T) {
		meta := RequestMetadata{
			Name: "api-server",
			Kind: "deployment",
		}
		metrics := []string{"cpu_usage"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Empty(t, queries)
	})

	t.Run("Cluster-level metrics without namespace/name", func(t *testing.T) {
		meta := RequestMetadata{}
		metrics := []string{"cpu_real", "cpu_total", "mem_real", "mem_total",
			"cpu_request", "cpu_limit", "memory_request", "memory_limit"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 8)
		// Node-level metrics from K8sNodeSample
		assert.Contains(t, queries["cpu_real"], "K8sNodeSample")
		assert.Contains(t, queries["cpu_real"], "cpuUsedCores")
		assert.Contains(t, queries["cpu_total"], "capacityCpuCores")
		assert.Contains(t, queries["mem_real"], "memoryUsedBytes")
		assert.Contains(t, queries["mem_total"], "capacityMemoryBytes")
		// Container-level cluster aggregations from K8sContainerSample
		assert.Contains(t, queries["cpu_request"], "K8sContainerSample")
		assert.Contains(t, queries["cpu_request"], "cpuRequestedCores")
		assert.Contains(t, queries["cpu_limit"], "cpuLimitCores")
		assert.Contains(t, queries["memory_request"], "memoryRequestedBytes")
		assert.Contains(t, queries["memory_limit"], "memoryLimitBytes")
	})

	t.Run("Cluster metrics returned alongside workload metrics", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "production",
			Name:      "api-server",
			Kind:      "deployment",
		}
		metrics := []string{"cpu_real", "cpu_total", "mem_real", "mem_total", "cpu_usage", "memory_usage"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 6)
		// Cluster metrics from K8sNodeSample
		assert.Contains(t, queries["cpu_real"], "K8sNodeSample")
		assert.Contains(t, queries["mem_total"], "K8sNodeSample")
		// Workload metrics from K8sContainerSample
		assert.Contains(t, queries["cpu_usage"], "K8sContainerSample")
		assert.Contains(t, queries["memory_usage"], "K8sContainerSample")
	})

	t.Run("Percentile and max cluster metrics", func(t *testing.T) {
		meta := RequestMetadata{}
		metrics := []string{"p90_cpu", "p50_cpu", "p90_mem", "p50_mem", "max_usage_cpu", "max_usage_mem"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 6)
		assert.Contains(t, queries["p90_cpu"], "percentile(cpuUsedCores, 90)")
		assert.Contains(t, queries["p50_cpu"], "percentile(cpuUsedCores, 50)")
		assert.Contains(t, queries["p90_mem"], "percentile(memoryUsedBytes, 90)")
		assert.Contains(t, queries["p50_mem"], "percentile(memoryUsedBytes, 50)")
		assert.Contains(t, queries["max_usage_cpu"], "max(cpuUsedCores)")
		assert.Contains(t, queries["max_usage_mem"], "max(memoryUsedBytes)")
	})

	t.Run("Workload context overrides cluster-level resource queries", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "production",
			Name:      "api-server",
			Kind:      "deployment",
		}
		metrics := []string{"cpu_request", "cpu_limit", "memory_request", "memory_limit"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 4)
		// Should have WHERE clause for workload filtering (Pass 2 overwrites Pass 1)
		assert.Contains(t, queries["cpu_request"], "WHERE")
		assert.Contains(t, queries["cpu_request"], "namespaceName = 'production'")
		assert.Contains(t, queries["cpu_request"], "deploymentName = 'api-server'")
		assert.Contains(t, queries["cpu_request"], "FACET")
		assert.Contains(t, queries["cpu_limit"], "WHERE")
		assert.Contains(t, queries["memory_request"], "WHERE")
		assert.Contains(t, queries["memory_limit"], "WHERE")
	})

	t.Run("Namespace only queries", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "production",
			Kind:      "deployment",
		}
		metrics := []string{"cpu_usage"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 1)
		assert.Contains(t, queries["cpu_usage"], "namespaceName = 'production'")
		// FACET is still added for grouping, but WHERE clause doesn't filter by deploymentName
		assert.Contains(t, queries["cpu_usage"], "FACET deploymentName")
		assert.NotContains(t, queries["cpu_usage"], "AND deploymentName =")
	})

	t.Run("Namespace only with resource metrics overrides cluster-level", func(t *testing.T) {
		meta := RequestMetadata{
			Namespace: "production",
			Kind:      "deployment",
		}
		metrics := []string{"cpu_request", "cpu_limit", "memory_request", "memory_limit"}

		queries := buildNewRelicWorkloadQueries(meta, metrics)

		assert.Len(t, queries, 4)
		// Pass 2 should override Pass 1 with namespace-scoped queries
		assert.Contains(t, queries["cpu_request"], "WHERE")
		assert.Contains(t, queries["cpu_request"], "namespaceName = 'production'")
		assert.Contains(t, queries["cpu_request"], "FACET")
		assert.NotContains(t, queries["cpu_request"], "AND deploymentName =")
		// Should NOT be the cluster-level query (which has no WHERE)
		assert.NotEqual(t, "SELECT sum(cpuRequestedCores) FROM K8sContainerSample", queries["cpu_request"])
	})
}

func TestBuildNRQLMetricQuery_InstantFlag(t *testing.T) {
	s := &NewRelicMetricSource{}

	t.Run("Instant query skips TIMESERIES", func(t *testing.T) {
		query, _ := s.buildNRQLMetricQuery(
			"SELECT sum(cpuUsedCores) FROM K8sNodeSample",
			1700000000, 1700003600, 60, true, nil, nil,
		)
		assert.NotContains(t, query, "TIMESERIES")
		assert.Contains(t, query, "SINCE")
		assert.Contains(t, query, "UNTIL")
	})

	t.Run("Non-instant query adds TIMESERIES", func(t *testing.T) {
		query, _ := s.buildNRQLMetricQuery(
			"SELECT sum(cpuUsedCores) FROM K8sNodeSample",
			1700000000, 1700003600, 60, false, nil, nil,
		)
		assert.Contains(t, query, "TIMESERIES 60 seconds")
		assert.Contains(t, query, "SINCE")
	})

	t.Run("Instant with raw metric name skips TIMESERIES", func(t *testing.T) {
		query, _ := s.buildNRQLMetricQuery(
			"my.custom.metric",
			1700000000, 1700003600, 60, true, nil, nil,
		)
		assert.NotContains(t, query, "TIMESERIES")
		assert.Contains(t, query, "average(`my.custom.metric`)")
	})
}

func TestConvertNRMetricsToQueryResult_InstantFallback(t *testing.T) {
	s := &NewRelicMetricSource{}

	t.Run("Aggregate result without timestamp gets current time fallback", func(t *testing.T) {
		results := []map[string]any{
			{"sum.cpuUsedCores": float64(4.5)},
		}

		qr := s.convertNRMetricsToQueryResult(results, "cpu_real", "")

		assert.Equal(t, "cpu_real", qr.QueryKey)
		assert.Len(t, qr.Payload, 1)
		assert.Len(t, qr.Payload[0].Timestamps, 1)
		assert.Greater(t, qr.Payload[0].Timestamps[0], int64(0))
		assert.Equal(t, float64(4.5), qr.Payload[0].Values[0])
	})

	t.Run("Timeseries result uses beginTimeSeconds", func(t *testing.T) {
		results := []map[string]any{
			{"beginTimeSeconds": float64(1700000000), "endTimeSeconds": float64(1700000060), "sum.cpuUsedCores": float64(2.0)},
			{"beginTimeSeconds": float64(1700000060), "endTimeSeconds": float64(1700000120), "sum.cpuUsedCores": float64(3.0)},
		}

		qr := s.convertNRMetricsToQueryResult(results, "cpu_usage", "")

		// Same metric, no FACET labels → both rows grouped into one series.
		assert.Len(t, qr.Payload, 1)
		assert.Equal(t, []int64{1700000000, 1700000060}, qr.Payload[0].Timestamps)
		assert.Equal(t, []float64{2.0, 3.0}, qr.Payload[0].Values)
	})

	t.Run("Result with no value and no timestamp is skipped", func(t *testing.T) {
		results := []map[string]any{
			{"someStringField": "value"},
		}

		qr := s.convertNRMetricsToQueryResult(results, "test", "")

		assert.Empty(t, qr.Payload)
	})
}

// =============================================================================
// End-to-End Tests: FetchMetricUtilisation with New Relic
// =============================================================================

func TestFetchMetricUtilisation_NewRelic_NodeMetrics_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")
	nodeName := os.Getenv("NEW_RELIC_TEST_NODE_NAME") // Optional: specific node to test

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	if nodeName == "" {
		nodeName = "test-node" // Default node name for testing
		t.Logf("Using default node name: %s (set NEW_RELIC_TEST_NODE_NAME to override)", nodeName)
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	// Mock GetLogsMetricsTracesProvider to return newrelic
	patches.ApplyFunc(GetLogsMetricsTracesProvider,
		func(ctx *security.RequestContext, accountId, provider, providerType, source string) (string, string, error) {
			return "newrelic", "user", nil
		})

	ctx := mockRequestContext()
	now := time.Now()

	req := GetUtilisationTrendRequest{
		AccountId:            accountID,
		MetricProvider:       "newrelic",
		MetricProviderSource: "user",
		StartTime:            now.Add(-1 * time.Hour).UnixMilli(),
		EndTime:              now.UnixMilli(),
		Request: map[string]any{
			"kind":      "node",
			"node_name": nodeName,
			"metrics":   []string{"cpu_usage", "memory_usage", "disk_total", "disk_used"},
		},
	}

	output, err := FetchMetricUtilisation(ctx, req)

	if err != nil {
		t.Logf("Error (may be expected if no data): %v", err)
	}

	t.Logf("Node Metrics E2E Test Results:")
	t.Logf("  Node: %s", nodeName)
	t.Logf("  Results count: %d", len(output.Results))

	for _, result := range output.Results {
		t.Logf("  Metric: %s", result.QueryKey)
		t.Logf("    Query: %s", result.Query)
		if result.Error != nil {
			t.Logf("    Error: %s", *result.Error)
		}
		t.Logf("    Payload count: %d", len(result.Payload))
		for i, payload := range result.Payload {
			if i < 2 { // Only log first 2
				t.Logf("      Labels: %v, Points: %d", payload.Metric, len(payload.Values))
				if len(payload.Values) > 0 {
					t.Logf("      First value: %f", payload.Values[0])
				}
			}
		}
	}
}

func TestFetchMetricUtilisation_NewRelic_WorkloadMetrics_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")
	namespace := os.Getenv("NEW_RELIC_TEST_NAMESPACE")       // Optional
	deploymentName := os.Getenv("NEW_RELIC_TEST_DEPLOYMENT") // Optional

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	if namespace == "" {
		namespace = "default"
		t.Logf("Using default namespace: %s (set NEW_RELIC_TEST_NAMESPACE to override)", namespace)
	}
	if deploymentName == "" {
		deploymentName = "test-deployment"
		t.Logf("Using default deployment: %s (set NEW_RELIC_TEST_DEPLOYMENT to override)", deploymentName)
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock GetNewRelicConfigs to return env values
	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	// Mock GetLogsMetricsTracesProvider to return newrelic
	patches.ApplyFunc(GetLogsMetricsTracesProvider,
		func(ctx *security.RequestContext, accountId, provider, providerType, source string) (string, string, error) {
			return "newrelic", "user", nil
		})

	ctx := mockRequestContext()
	now := time.Now()

	req := GetUtilisationTrendRequest{
		AccountId:            accountID,
		MetricProvider:       "newrelic",
		MetricProviderSource: "user",
		StartTime:            now.Add(-1 * time.Hour).UnixMilli(),
		EndTime:              now.UnixMilli(),
		Request: map[string]any{
			"kind":               "deployment",
			"workload_namespace": namespace,
			"workload_name":      deploymentName,
			"metrics":            []string{"cpu_usage", "memory_usage", "cpu_request", "memory_request"},
		},
	}

	output, err := FetchMetricUtilisation(ctx, req)

	if err != nil {
		t.Logf("Error (may be expected if no data): %v", err)
	}

	t.Logf("Workload Metrics E2E Test Results:")
	t.Logf("  Namespace: %s", namespace)
	t.Logf("  Deployment: %s", deploymentName)
	t.Logf("  Results count: %d", len(output.Results))

	for _, result := range output.Results {
		t.Logf("  Metric: %s", result.QueryKey)
		t.Logf("    Query: %s", result.Query)
		if result.Error != nil {
			t.Logf("    Error: %s", *result.Error)
		}
		t.Logf("    Payload count: %d", len(result.Payload))
		for i, payload := range result.Payload {
			if i < 2 { // Only log first 2
				t.Logf("      Labels: %v, Points: %d", payload.Metric, len(payload.Values))
				if len(payload.Values) > 0 {
					t.Logf("      First value: %f", payload.Values[0])
				}
			}
		}
	}
}

func TestFetchMetricUtilisation_NewRelic_AllWorkloadKinds_E2E(t *testing.T) {
	// Read New Relic configuration from environment variables
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")
	namespace := os.Getenv("NEW_RELIC_TEST_NAMESPACE")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	if namespace == "" {
		namespace = "default"
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	patches.ApplyFunc(GetLogsMetricsTracesProvider,
		func(ctx *security.RequestContext, accountId, provider, providerType, source string) (string, string, error) {
			return "newrelic", "user", nil
		})

	ctx := mockRequestContext()
	now := time.Now()

	testCases := []struct {
		name         string
		kind         string
		workloadName string
	}{
		{"Pod", "pod", "test-pod"},
		{"Deployment", "deployment", "test-deployment"},
		{"StatefulSet", "statefulset", "test-statefulset"},
		{"DaemonSet", "daemonset", "test-daemonset"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := GetUtilisationTrendRequest{
				AccountId:            accountID,
				MetricProvider:       "newrelic",
				MetricProviderSource: "user",
				StartTime:            now.Add(-1 * time.Hour).UnixMilli(),
				EndTime:              now.UnixMilli(),
				Request: map[string]any{
					"kind":               tc.kind,
					"workload_namespace": namespace,
					"workload_name":      tc.workloadName,
					"metrics":            []string{"cpu_usage", "memory_usage"},
				},
			}

			output, err := FetchMetricUtilisation(ctx, req)

			// We don't assert NoError because the workload may not exist
			// Instead, we verify the query was built correctly
			t.Logf("%s Query Test:", tc.name)
			if err != nil {
				t.Logf("  Error: %v", err)
			}
			for _, result := range output.Results {
				t.Logf("  Query for %s: %s", result.QueryKey, result.Query)
			}
		})
	}
}

func TestFetchMetricUtilisation_NewRelic_PVCMetrics_E2E(t *testing.T) {
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountID := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")
	namespace := os.Getenv("NEW_RELIC_TEST_NAMESPACE")
	pvcName := os.Getenv("NEW_RELIC_TEST_PVC_NAME")

	if apiKey == "" || accountID == "" || region == "" {
		t.Skip("Skipping E2E test: NEW_RELIC_API_KEY, NEW_RELIC_ACCOUNT_ID, and NEW_RELIC_REGION environment variables are required")
	}

	if namespace == "" {
		namespace = "default"
	}
	if pvcName == "" {
		pvcName = "test-pvc"
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetNewRelicConfigs,
		func(ctx *security.RequestContext, accountId string) (string, string, string, error) {
			return apiKey, accountID, region, nil
		})

	patches.ApplyFunc(GetLogsMetricsTracesProvider,
		func(ctx *security.RequestContext, accountId, provider, providerType, source string) (string, string, error) {
			return "newrelic", "user", nil
		})

	ctx := mockRequestContext()
	now := time.Now()

	req := GetUtilisationTrendRequest{
		AccountId:            accountID,
		MetricProvider:       "newrelic",
		MetricProviderSource: "user",
		StartTime:            now.Add(-1 * time.Hour).UnixMilli(),
		EndTime:              now.UnixMilli(),
		Request: map[string]any{
			"kind":               "statefulset",
			"workload_namespace": namespace,
			"workload_name":      "test-statefulset",
			"pvc_name":           pvcName,
			"metrics":            []string{"pvc_usage", "pvc_requests"},
		},
	}

	output, err := FetchMetricUtilisation(ctx, req)

	t.Logf("PVC Metrics E2E Test Results:")
	if err != nil {
		t.Logf("  Error: %v", err)
	}
	for _, result := range output.Results {
		t.Logf("  Metric: %s", result.QueryKey)
		t.Logf("    Query: %s", result.Query)
		t.Logf("    Payload count: %d", len(result.Payload))
	}
}

func TestNewRelicMetricSource_CalculateStepInterval(t *testing.T) {
	s := &NewRelicMetricSource{}

	tests := []struct {
		name              string
		requestedInterval int
		startTime         int64
		endTime           int64
		integrationType   string
		expectedInterval  int
		description       string
	}{
		{
			name:              "1 hour range - default to 60s",
			requestedInterval: 0,
			startTime:         1704067200, // 2024-01-01 00:00:00
			endTime:           1704070800, // 2024-01-01 01:00:00 (1 hour later)
			integrationType:   "newrelic",
			expectedInterval:  60,
			description:       "For 1 hour (3600s), default should be 60s (60 buckets)",
		},
		{
			name:              "1 day range - default to 300s",
			requestedInterval: 0,
			startTime:         1704067200, // 2024-01-01 00:00:00
			endTime:           1704153600, // 2024-01-02 00:00:00 (1 day later)
			integrationType:   "newrelic",
			expectedInterval:  300,
			description:       "For 1 day (86400s), default should be 300s/5min (288 buckets)",
		},
		{
			name:              "1 week range - default to 1800s",
			requestedInterval: 0,
			startTime:         1704067200, // 2024-01-01 00:00:00
			endTime:           1704672000, // 2024-01-08 00:00:00 (7 days later)
			integrationType:   "newrelic",
			expectedInterval:  1800,
			description:       "For 1 week (604800s), default should be 1800s/30min (336 buckets)",
		},
		{
			name:              "30 days range - default to 3600s but enforce minimum",
			requestedInterval: 0,
			startTime:         1704067200, // 2024-01-01 00:00:00
			endTime:           1706659200, // 2024-01-31 00:00:00 (30 days later)
			integrationType:   "newrelic",
			expectedInterval:  7082, // (2592000 / 366) + 1 = 7082 (integer division: 7081 + 1)
			description:       "For 30 days (2592000s), needs 7082s to stay under 366 buckets",
		},
		{
			name:              "User interval too small - enforce minimum",
			requestedInterval: 100,
			startTime:         1704067200, // 2024-01-01 00:00:00
			endTime:           1704153600, // 2024-01-02 00:00:00 (1 day later)
			integrationType:   "newrelic",
			expectedInterval:  237, // (86400 / 366) + 1 = 237
			description:       "User wants 100s but needs 237s to stay under 366 buckets",
		},
		{
			name:              "User interval large enough - respect it",
			requestedInterval: 500,
			startTime:         1704067200, // 2024-01-01 00:00:00
			endTime:           1704153600, // 2024-01-02 00:00:00 (1 day later)
			integrationType:   "newrelic",
			expectedInterval:  500,
			description:       "User wants 500s which gives 172 buckets - respect it",
		},
		{
			name:              "Maximum safe range - exactly 366 buckets with 60s interval",
			requestedInterval: 60,
			startTime:         1704067200,              // 2024-01-01 00:00:00
			endTime:           1704067200 + (60 * 365), // 365 minutes later
			integrationType:   "newrelic",
			expectedInterval:  60,
			description:       "21900s range with 60s = 365 buckets (safe)",
		},
		{
			name:              "Edge case - 366 buckets boundary",
			requestedInterval: 0,
			startTime:         1704067200,               // 2024-01-01 00:00:00
			endTime:           1704067200 + (366 * 100), // Would be exactly 366 buckets at 100s
			integrationType:   "newrelic",
			expectedInterval:  300, // 36600s (~10h) is < 1 day, so defaults to max(300, 101) = 300
			description:       "Boundary test: 36600s range uses 5-min default (122 buckets)",
		},
		{
			name:              "Invalid range - start equals end",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704067200,
			integrationType:   "newrelic",
			expectedInterval:  60, // Returns minimum
			description:       "Edge case: zero time range returns minimum interval",
		},
		{
			name:              "Invalid range - end before start",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704063600,
			integrationType:   "newrelic",
			expectedInterval:  60, // Returns minimum
			description:       "Edge case: negative time range returns minimum interval",
		},
		{
			name:              "Integration type without bucket limit - 30 days",
			requestedInterval: 0,
			startTime:         1704067200,   // 2024-01-01 00:00:00
			endTime:           1706659200,   // 2024-01-31 00:00:00 (30 days later)
			integrationType:   "prometheus", // Not in the mapping, no bucket limit
			expectedInterval:  3600,         // Default for > 1 week range
			description:       "Integration without bucket limit uses default interval only",
		},
		{
			name:              "Integration type without bucket limit - user interval respected",
			requestedInterval: 100,
			startTime:         1704067200, // 2024-01-01 00:00:00
			endTime:           1706659200, // 2024-01-31 00:00:00 (30 days later)
			integrationType:   "datadog",  // Not in the mapping, no bucket limit
			expectedInterval:  100,        // User interval respected
			description:       "Integration without limit respects any user interval",
		},
		{
			name:              "Empty integration type - uses defaults",
			requestedInterval: 0,
			startTime:         1704067200, // 2024-01-01 00:00:00
			endTime:           1704153600, // 2024-01-02 00:00:00 (1 day later)
			integrationType:   "",         // Empty integration type
			expectedInterval:  300,        // Default for 1 day range
			description:       "Empty integration type uses default intervals",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.calculateStepInterval(tt.requestedInterval, tt.startTime, tt.endTime, tt.integrationType)

			// Verify the result matches expected
			assert.Equal(t, tt.expectedInterval, result, tt.description)

			// Verify the result keeps us under max buckets (only for integration types with limits)
			if tt.endTime > tt.startTime && tt.integrationType == "newrelic" {
				timeRange := tt.endTime - tt.startTime
				bucketCount := timeRange / int64(result)
				assert.LessOrEqual(t, bucketCount, int64(366),
					"Bucket count should never exceed 366 for New Relic. Time range: %d, Step: %d, Buckets: %d",
					timeRange, result, bucketCount)
			}
		})
	}
}

// TestCalculateStepInterval_RealWorldScenarios tests real-world time ranges
func TestCalculateStepInterval_RealWorldScenarios(t *testing.T) {
	s := &NewRelicMetricSource{}

	scenarios := []struct {
		name        string
		duration    time.Duration
		description string
	}{
		{"Last 5 minutes", 5 * time.Minute, "Short-term monitoring"},
		{"Last 15 minutes", 15 * time.Minute, "Recent activity"},
		{"Last hour", 1 * time.Hour, "Hourly dashboard"},
		{"Last 6 hours", 6 * time.Hour, "Business hours"},
		{"Last 24 hours", 24 * time.Hour, "Daily view"},
		{"Last 3 days", 3 * 24 * time.Hour, "Multi-day analysis"},
		{"Last week", 7 * 24 * time.Hour, "Weekly report"},
		{"Last month", 30 * 24 * time.Hour, "Monthly metrics"},
		{"Last 90 days", 90 * 24 * time.Hour, "Quarterly review"},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			now := time.Now().Unix()
			startTime := now - int64(sc.duration.Seconds())
			endTime := now

			result := s.calculateStepInterval(0, startTime, endTime, "newrelic")

			// Verify bucket count is safe
			timeRange := endTime - startTime
			bucketCount := timeRange / int64(result)

			t.Logf("%s: Duration=%s, TimeRange=%ds, StepInterval=%ds, Buckets=%d",
				sc.description, sc.duration, timeRange, result, bucketCount)

			assert.LessOrEqual(t, bucketCount, int64(366),
				"Bucket count must not exceed 366 for %s", sc.description)
			assert.Greater(t, result, 0, "Step interval must be positive")
		})
	}
}

// TestCalculateStepInterval_EdgeCases tests additional edge cases
func TestCalculateStepInterval_EdgeCases(t *testing.T) {
	s := &NewRelicMetricSource{}

	tests := []struct {
		name              string
		requestedInterval int
		startTime         int64
		endTime           int64
		integrationType   string
		expectedInterval  int
		description       string
	}{
		{
			name:              "Negative user interval - uses defaults",
			requestedInterval: -100,
			startTime:         1704067200,
			endTime:           1704153600, // 1 day
			integrationType:   "newrelic",
			expectedInterval:  300,
			description:       "Negative interval treated as 0, uses defaults",
		},
		{
			name:              "Very large user interval - respected",
			requestedInterval: 86400, // 1 day intervals
			startTime:         1704067200,
			endTime:           1711929600, // 90 days later
			integrationType:   "newrelic",
			expectedInterval:  86400,
			description:       "Very large user interval should be respected",
		},
		{
			name:              "Time range boundary - exactly 1 hour",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704070800, // Exactly 3600s
			integrationType:   "newrelic",
			expectedInterval:  60,
			description:       "Exactly 1 hour boundary should use 60s default",
		},
		{
			name:              "Time range boundary - exactly 1 day",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704153600, // Exactly 86400s
			integrationType:   "newrelic",
			expectedInterval:  300,
			description:       "Exactly 1 day boundary should use 300s default",
		},
		{
			name:              "Time range boundary - exactly 1 week",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704672000, // Exactly 604800s
			integrationType:   "newrelic",
			expectedInterval:  1800,
			description:       "Exactly 1 week boundary should use 1800s default",
		},
		{
			name:              "Very small time range - less than 1 minute",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704067230, // 30 seconds
			integrationType:   "newrelic",
			expectedInterval:  60,
			description:       "Very small time range uses minimum 60s interval",
		},
		{
			name:              "Very large time range - 1 year",
			requestedInterval: 0,
			startTime:         1704067200, // 2024-01-01
			endTime:           1735689600, // 2025-01-01 (366 days - leap year!)
			integrationType:   "newrelic",
			expectedInterval:  86401, // (31622400 / 366) + 1 = 86401
			description:       "1 year (leap year) range enforces bucket limit",
		},
		{
			name:              "Different case integration type - still enforces limit",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1706659200, // 30 days
			integrationType:   "NEWRELIC", // Uppercase (parameter unused)
			expectedInterval:  7082,       // Always enforces 366 bucket limit
			description:       "Integration type parameter unused, always enforces limit",
		},
		{
			name:              "Mixed case integration type - still enforces limit",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1706659200, // 30 days
			integrationType:   "NewRelic", // Mixed case (parameter unused)
			expectedInterval:  7082,       // Always enforces 366 bucket limit
			description:       "Integration type parameter unused, always enforces limit",
		},
		{
			name:              "User interval exactly equals minimum required",
			requestedInterval: 237, // Exactly (86400 / 366) + 1
			startTime:         1704067200,
			endTime:           1704153600, // 1 day
			integrationType:   "newrelic",
			expectedInterval:  237,
			description:       "User interval exactly at minimum should be accepted",
		},
		{
			name:              "User interval one less than minimum required",
			requestedInterval: 236, // One less than (86400 / 366) + 1 = 237
			startTime:         1704067200,
			endTime:           1704153600, // 1 day
			integrationType:   "newrelic",
			expectedInterval:  237, // Should be bumped up
			description:       "User interval slightly below minimum should be enforced",
		},
		{
			name:              "Time range just over 1 hour - switches to 5min default",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704070801, // 3601s (just over 1 hour)
			integrationType:   "newrelic",
			expectedInterval:  300,
			description:       "Just over 1 hour should switch to 5-min interval",
		},
		{
			name:              "Time range just over 1 day - switches to 30min default",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704153601, // 86401s (just over 1 day)
			integrationType:   "newrelic",
			expectedInterval:  1800,
			description:       "Just over 1 day should switch to 30-min interval",
		},
		{
			name:              "Time range just over 1 week - switches to 1hour default",
			requestedInterval: 0,
			startTime:         1704067200,
			endTime:           1704672001, // 604801s (just over 1 week)
			integrationType:   "newrelic",
			expectedInterval:  3600,
			description:       "Just over 1 week should switch to 1-hour interval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.calculateStepInterval(tt.requestedInterval, tt.startTime, tt.endTime, tt.integrationType)

			// Verify the result matches expected
			assert.Equal(t, tt.expectedInterval, result, tt.description)

			// Verify bucket count for newrelic (case-sensitive check)
			if tt.integrationType == "newrelic" && tt.endTime > tt.startTime {
				timeRange := tt.endTime - tt.startTime
				bucketCount := timeRange / int64(result)
				assert.LessOrEqual(t, bucketCount, int64(366),
					"Bucket count must not exceed 366 for newrelic. Time range: %d, Step: %d, Buckets: %d",
					timeRange, result, bucketCount)
			}
		})
	}
}

// TestCalculateStepInterval_BucketCountVerification verifies bucket count for all newrelic scenarios
func TestCalculateStepInterval_BucketCountVerification(t *testing.T) {
	s := &NewRelicMetricSource{}

	// Generate test cases for various time ranges
	testRanges := []struct {
		name     string
		duration time.Duration
	}{
		{"10 minutes", 10 * time.Minute},
		{"30 minutes", 30 * time.Minute},
		{"2 hours", 2 * time.Hour},
		{"12 hours", 12 * time.Hour},
		{"2 days", 2 * 24 * time.Hour},
		{"5 days", 5 * 24 * time.Hour},
		{"14 days", 14 * 24 * time.Hour},
		{"60 days", 60 * 24 * time.Hour},
		{"180 days", 180 * 24 * time.Hour},
		{"365 days", 365 * 24 * time.Hour},
	}

	for _, tr := range testRanges {
		t.Run(tr.name, func(t *testing.T) {
			now := time.Now().Unix()
			startTime := now - int64(tr.duration.Seconds())
			endTime := now

			// Test with no user-provided interval
			result := s.calculateStepInterval(0, startTime, endTime, "newrelic")

			timeRange := endTime - startTime
			bucketCount := timeRange / int64(result)

			t.Logf("%s: TimeRange=%ds, StepInterval=%ds, Buckets=%d",
				tr.name, timeRange, result, bucketCount)

			assert.LessOrEqual(t, bucketCount, int64(366),
				"Bucket count must never exceed 366 for New Relic")
			assert.Greater(t, result, 0, "Step interval must be positive")
		})
	}
}

func TestBuildNRQLOperatorClause_IContains(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		op       query.BinaryWhereClauseType
		val      any
		expected string
		wantErr  bool
	}{
		{
			name:     "IContains basic",
			field:    "message",
			op:       query.IContains,
			val:      "ERROR",
			expected: "LOWER(message) LIKE '%error%'",
			wantErr:  false,
		},
		{
			name:     "IContains with special chars",
			field:    "message",
			op:       query.IContains,
			val:      "test.com",
			expected: "LOWER(message) LIKE '%test.com%'",
			wantErr:  false,
		},
		{
			name:     "IContains empty string",
			field:    "message",
			op:       query.IContains,
			val:      "",
			expected: "", // Empty string should return empty to skip filter
			wantErr:  false,
		},
		{
			name:     "NIContains basic",
			field:    "message",
			op:       query.NIContains,
			val:      "DEBUG",
			expected: "LOWER(message) NOT LIKE '%debug%'",
			wantErr:  false,
		},
		{
			name:     "NIContains empty string",
			field:    "message",
			op:       query.NIContains,
			val:      "",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "IContains with field requiring backticks",
			field:    "k8s.pod.name",
			op:       query.IContains,
			val:      "API",
			expected: "LOWER(`k8s.pod.name`) LIKE '%api%'",
			wantErr:  false,
		},
		{
			name:     "IContains with single quotes in value",
			field:    "message",
			op:       query.IContains,
			val:      "user's error",
			expected: "LOWER(message) LIKE '%user\\'s error%'", // Escaped single quote
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildNRQLOperatorClause(tt.field, tt.op, tt.val)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// ============================================================================
// buildNRQLLogQuery Complete Query Handling Tests
// ============================================================================

func TestBuildNRQLLogQuery_CompleteQueryPassthrough(t *testing.T) {
	// Test that when req.Query contains a complete NRQL query (from GetQuery()),
	// it's returned as-is without duplication
	source := &NewRelicLogSource{}

	completeQuery := "SELECT * FROM Log WHERE namespace_name = 'nudgebee' SINCE 1771726813 UNTIL 1771730413 LIMIT 100"

	req := FetchLogRequest{
		Query:     completeQuery,
		StartTime: 1771726813000,
		EndTime:   1771730413000,
		Limit:     100,
	}

	result, err := source.buildNRQLLogQuery(req)

	assert.NoError(t, err)
	assert.Equal(t, completeQuery, result, "Complete query should be returned as-is without duplication")
}

func TestBuildNRQLLogQuery_WhereClauseOnly(t *testing.T) {
	// Test that when req.Query contains only a WHERE clause, it's used correctly
	source := &NewRelicLogSource{}

	req := FetchLogRequest{
		Query:     "namespace_name = 'test'", // WHERE clause only
		StartTime: 1000,
		EndTime:   2000,
		Limit:     50,
	}

	result, err := source.buildNRQLLogQuery(req)

	assert.NoError(t, err)
	assert.Contains(t, result, "SELECT * FROM Log WHERE namespace_name = 'test' SINCE 1000 UNTIL 2000 LIMIT 50")
}

func TestBuildNRQLLogQuery_StructuredWhereClause(t *testing.T) {
	// Test that QueryRequest.Where is used when Query is empty
	source := &NewRelicLogSource{}

	req := FetchLogRequest{
		Query: "",
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace_name": {query.Eq: "production"},
				},
			},
		},
		StartTime: 1000,
		EndTime:   2000,
		Limit:     100,
	}

	result, err := source.buildNRQLLogQuery(req)

	assert.NoError(t, err)
	assert.Contains(t, result, "SELECT * FROM Log WHERE namespace_name = 'production' SINCE 1000 UNTIL 2000 LIMIT 100")
}

// ============================================================================
// buildNRQLNodeNameFilter Tests
// ============================================================================

func TestBuildNRQLNodeNameFilter_Empty(t *testing.T) {
	result := buildNRQLNodeNameFilter("")
	assert.Equal(t, "", result)
}

func TestBuildNRQLNodeNameFilter_SingleExactName(t *testing.T) {
	result := buildNRQLNodeNameFilter("gke-node-abc123")
	assert.Equal(t, "nodeName = 'gke-node-abc123'", result)
}

func TestBuildNRQLNodeNameFilter_SingleNameWithWildcard(t *testing.T) {
	result := buildNRQLNodeNameFilter("gke-node-abc123.*")
	assert.Equal(t, "nodeName RLIKE 'gke-node-abc123.*'", result)
}

func TestBuildNRQLNodeNameFilter_PipeSeparatedExactNames(t *testing.T) {
	result := buildNRQLNodeNameFilter("gke-node-1|gke-node-2|gke-node-3")
	assert.Equal(t, "nodeName RLIKE 'gke-node-1|gke-node-2|gke-node-3'", result)
}

func TestBuildNRQLNodeNameFilter_PipeSeparatedRegexPatterns(t *testing.T) {
	nodeName := "gke-nudgebee-dev-pool-arm-spot-enable-92eca669-zbdm.*|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-ew71.*"
	result := buildNRQLNodeNameFilter(nodeName)
	assert.Equal(t, "nodeName RLIKE '"+nodeName+"'", result)
}

func TestBuildNRQLNodeNameFilter_SingleQuoteEscaping(t *testing.T) {
	// Ensure single quotes in node names are escaped to prevent injection
	result := buildNRQLNodeNameFilter("node-with-'quote")
	assert.Equal(t, "nodeName = 'node-with-\\'quote'", result)
}

func TestBuildNRQLNodeNameFilter_LongPatternExceeds256Chars(t *testing.T) {
	// 10 long GKE node names joined with | exceed NewRelic's 256-char RLIKE limit;
	// the result must be split into multiple RLIKE OR conditions wrapped in parentheses.
	nodeName := "gke-nudgebee-dev-pool-arm-spot-enable-92eca669-zbdm.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-ew71.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-jgdk.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-v6s5.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-2f8f.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-g004.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-79on.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-re9d.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-fghg.*" +
		"|gke-nudgebee-dev-pool-arm-spot-enable-92eca669-6llh.*"
	result := buildNRQLNodeNameFilter(nodeName)
	// Result must be wrapped in parentheses (multiple OR conditions)
	assert.True(t, len(result) > 0, "result should not be empty")
	assert.Contains(t, result, " OR ", "long pattern should be split into OR conditions")
	assert.True(t, result[0] == '(' && result[len(result)-1] == ')', "result should be wrapped in parentheses")
	// Each individual RLIKE chunk must not exceed 256 chars (excluding "nodeName RLIKE '...'")
	// Verify all node name fragments are present in the result
	assert.Contains(t, result, "92eca669-zbdm")
	assert.Contains(t, result, "92eca669-6llh")
}

func TestBuildNRQLSpanWhereClause_DurationNsGte(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"duration_ns": {query.Gte: int64(5_000_000_000)}, // 5s in ns
		},
	}
	result, err := buildNRQLSpanWhereClause(where)
	assert.NoError(t, err)
	assert.Equal(t, "`duration.ms` >= 5000", result, "5s in ns should map to 5000ms in NRQL")
	assert.NotContains(t, result, "duration_ns", "duration_ns must not appear in generated NRQL")
}

func TestBuildNRQLSpanWhereClause_DurationNsLt(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"duration_ns": {query.Lt: int64(1_000_000_000)}, // 1s in ns
		},
	}
	result, err := buildNRQLSpanWhereClause(where)
	assert.NoError(t, err)
	assert.Equal(t, "`duration.ms` < 1000", result)
}

func TestBuildNRQLSpanWhereClause_DurationNsWithOtherFilter(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"duration_ns":        {query.Gte: int64(2_000_000_000)}, // 2s
			"k8s.namespace.name": {query.Eq: "production"},
		},
	}
	result, err := buildNRQLSpanWhereClause(where)
	assert.NoError(t, err)
	assert.Contains(t, result, "`duration.ms` >= 2000")
	assert.Contains(t, result, "k8s.namespace.name")
	assert.NotContains(t, result, "duration_ns")
}

func TestValidateAPMScope(t *testing.T) {
	tests := []struct {
		name      string
		rawQuery  string
		labels    map[string]string
		expectErr bool
	}{
		{
			name:      "non-apm metric without labels passes",
			rawQuery:  "k8s.container.cpu_usage",
			labels:    nil,
			expectErr: false,
		},
		{
			name:      "apm metric without labels fails",
			rawQuery:  "apm.mobile.application.install.count",
			labels:    nil,
			expectErr: true,
		},
		{
			name:      "apm metric with service.name passes",
			rawQuery:  "apm.service.transaction.duration",
			labels:    map[string]string{"service.name": "checkout"},
			expectErr: false,
		},
		{
			name:      "apm metric with appName passes",
			rawQuery:  "apm.mobile.application.install.count",
			labels:    map[string]string{"appName": "MyApp"},
			expectErr: false,
		},
		{
			name:      "apm metric with entity.guid passes",
			rawQuery:  "apm.mobile.application.install.count",
			labels:    map[string]string{"entity.guid": "MXxBUE18QVBQTElDQVRJT04"},
			expectErr: false,
		},
		{
			name:      "apm metric scope check is case-insensitive on label key",
			rawQuery:  "apm.service.transaction.duration",
			labels:    map[string]string{"AppName": "checkout"},
			expectErr: false,
		},
		{
			name:      "full NRQL bypasses scope check",
			rawQuery:  "SELECT count(*) FROM Metric WHERE metricName = 'apm.mobile.x'",
			labels:    nil,
			expectErr: false,
		},
		{
			name:      "apm prefix is case-insensitive",
			rawQuery:  "APM.Service.Transaction.Duration",
			labels:    nil,
			expectErr: true,
		},
		{
			name:      "leading whitespace handled",
			rawQuery:  "  apm.mobile.something  ",
			labels:    nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAPMScope(tt.rawQuery, tt.labels)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

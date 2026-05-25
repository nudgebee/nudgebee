package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- buildSpanDQL ----

func TestBuildSpanDQL_NoTraceID_NoQuery(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{}
	dql, err := s.buildSpanDQL(req, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.NoError(t, err)
	assert.Contains(t, dql, "fetch spans")
	assert.Contains(t, dql, "sort start_time desc")
	assert.Contains(t, dql, "limit 100")
	assert.NotContains(t, dql, "filter")
}

func TestBuildSpanDQL_NoTraceID_WithQuery(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{Query: `k8s.workload.name == "my-service"`}
	dql, err := s.buildSpanDQL(req, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.NoError(t, err)
	assert.Contains(t, dql, `| filter k8s.workload.name == "my-service"`)
	assert.Contains(t, dql, "sort start_time desc")
}

func TestBuildSpanDQL_WithTraceID(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"trace_id": {query.Eq: "abc123xyz"},
				},
			},
		},
	}
	dql, err := s.buildSpanDQL(req, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.NoError(t, err)
	assert.Contains(t, dql, `filter trace.id == touid("abc123xyz")`)
	assert.Contains(t, dql, "sort start_time asc")
	assert.NotContains(t, dql, "limit")
}

func TestBuildSpanDQL_FromToPresent(t *testing.T) {
	s := &DynatraceTraceSource{}
	from := "2024-01-01T00:00:00Z"
	to := "2024-01-01T01:00:00Z"
	dql, err := s.buildSpanDQL(TracesV3Request{}, from, to)
	require.NoError(t, err)
	assert.Contains(t, dql, from)
	assert.Contains(t, dql, to)
}

func TestBuildSpanDQL_DurationNsFilterMappedToDuration(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"duration_ns": {query.Gte: int64(5000000000)},
				},
			},
		},
	}
	dql, err := s.buildSpanDQL(req, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.NoError(t, err)
	assert.Contains(t, dql, "duration >= 5000000000", "duration_ns must be mapped to the Dynatrace DQL field 'duration'")
	assert.NotContains(t, dql, "duration_ns", "unmapped field name duration_ns must not appear in generated DQL")
}

// ---- parseDurationNs ----

func TestParseDurationNs_Nil(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Equal(t, int64(0), s.parseDurationNs(nil))
}

func TestParseDurationNs_ValidString(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Equal(t, int64(4756000), s.parseDurationNs("4756000"))
}

func TestParseDurationNs_InvalidString(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Equal(t, int64(0), s.parseDurationNs("abc"))
}

func TestParseDurationNs_Float64(t *testing.T) {
	s := &DynatraceTraceSource{}
	// Decimal part is truncated
	assert.Equal(t, int64(4756000), s.parseDurationNs(float64(4756000.9)))
}

func TestParseDurationNs_Int64(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Equal(t, int64(9999), s.parseDurationNs(int64(9999)))
}

func TestParseDurationNs_NegativeString(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Equal(t, int64(0), s.parseDurationNs("-100"), "negative duration should clamp to 0")
}

func TestParseDurationNs_NegativeFloat(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Equal(t, int64(0), s.parseDurationNs(float64(-50)))
}

func TestParseDurationNs_Zero(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Equal(t, int64(0), s.parseDurationNs("0"))
}

// ---- mapSpanKind ----

func TestMapSpanKind(t *testing.T) {
	s := &DynatraceTraceSource{}
	tests := []struct {
		input    string
		expected string
	}{
		{"SERVER", "SERVER"}, {"server", "SERVER"}, {"ENTRY", "SERVER"}, {"entry", "SERVER"},
		{"CLIENT", "CLIENT"}, {"client", "CLIENT"}, {"EXIT", "CLIENT"}, {"exit", "CLIENT"},
		{"PRODUCER", "PRODUCER"}, {"producer", "PRODUCER"},
		{"CONSUMER", "CONSUMER"}, {"consumer", "CONSUMER"},
		{"INTERNAL", "INTERNAL"}, {"internal", "INTERNAL"}, {"LOCAL", "INTERNAL"}, {"local", "INTERNAL"},
		{"", "UNSPECIFIED"}, {"GATEWAY", "UNSPECIFIED"}, {"xyz", "UNSPECIFIED"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, s.mapSpanKind(tc.input))
		})
	}
}

// ---- extractTraceID ----

func TestExtractTraceID_NilBinary(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{} // Where.Binary is nil
	assert.Equal(t, "", s.extractTraceID(req))
}

func TestExtractTraceID_NoKey(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"other_field": {query.Eq: "some-value"},
				},
			},
		},
	}
	assert.Equal(t, "", s.extractTraceID(req))
}

func TestExtractTraceID_EmptyValue(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"trace_id": {query.Eq: ""},
				},
			},
		},
	}
	assert.Equal(t, "", s.extractTraceID(req))
}

func TestExtractTraceID_ValidValue(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"trace_id": {query.Eq: "abc123xyz"},
				},
			},
		},
	}
	assert.Equal(t, "abc123xyz", s.extractTraceID(req))
}

func TestExtractTraceID_NonStringValue(t *testing.T) {
	s := &DynatraceTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"trace_id": {query.Eq: 12345}, // non-string
				},
			},
		},
	}
	assert.Equal(t, "", s.extractTraceID(req))
}

// ---- collectLabelValues ----

func TestCollectLabelValues_Nil(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Nil(t, s.collectLabelValues(nil, "span.name", 50))
}

func TestCollectLabelValues_MissingLabel(t *testing.T) {
	s := &DynatraceTraceSource{}
	records := []map[string]any{
		{"other.field": "value"},
	}
	assert.Nil(t, s.collectLabelValues(records, "span.name", 50))
}

func TestCollectLabelValues_Dedup(t *testing.T) {
	s := &DynatraceTraceSource{}
	records := []map[string]any{
		{"span.name": "GET /api"},
		{"span.name": "GET /api"},
		{"span.name": "POST /data"},
	}
	result := s.collectLabelValues(records, "span.name", 50)
	assert.Equal(t, []string{"GET /api", "POST /data"}, result)
}

func TestCollectLabelValues_LimitEnforced(t *testing.T) {
	s := &DynatraceTraceSource{}
	records := make([]map[string]any, 20)
	for i := range records {
		records[i] = map[string]any{"label": string(rune('a' + i))}
	}
	result := s.collectLabelValues(records, "label", 3)
	assert.Len(t, result, 3)
}

func TestCollectLabelValues_EmptyStringSkipped(t *testing.T) {
	s := &DynatraceTraceSource{}
	records := []map[string]any{
		{"span.name": ""},
		{"span.name": "valid"},
	}
	result := s.collectLabelValues(records, "span.name", 50)
	assert.Equal(t, []string{"valid"}, result)
}

func TestCollectLabelValues_NonStringSkipped(t *testing.T) {
	s := &DynatraceTraceSource{}
	records := []map[string]any{
		{"span.name": int64(42)}, // non-string
		{"span.name": "real-value"},
	}
	result := s.collectLabelValues(records, "span.name", 50)
	assert.Equal(t, []string{"real-value"}, result)
}

// ---- recordToOTelSpan ----

func TestRecordToOTelSpan_AllFields(t *testing.T) {
	s := &DynatraceTraceSource{}
	record := map[string]any{
		"trace.id":                  "trace-001",
		"span.id":                   "span-001",
		"span.parent_id":            "parent-001",
		"span.name":                 "GET /api",
		"span.kind":                 "server",
		"k8s.workload.name":         "my-service",
		"http.response.status_code": "200",
		"duration":                  "4756000",
		"start_time":                "2024-01-01T00:00:00Z",
	}
	span := s.recordToOTelSpan(record)
	assert.Equal(t, "trace-001", span.TraceID)
	assert.Equal(t, "span-001", span.SpanID)
	assert.Equal(t, "parent-001", span.ParentSpanID)
	assert.Equal(t, "GET /api", span.SpanName)
	assert.Equal(t, "SERVER", span.SpanKind)
	assert.Equal(t, "my-service", span.ServiceName)
	assert.Equal(t, "200", span.StatusCode)
	assert.Equal(t, int64(4756000), span.DurationNs)
	assert.Equal(t, "2024-01-01T00:00:00Z", span.Timestamp)
}

func TestRecordToOTelSpan_FallbackFields(t *testing.T) {
	// When dot-notation fields are absent, underscore fallbacks are used.
	s := &DynatraceTraceSource{}
	record := map[string]any{
		"trace_id": "trace-fallback",
		"span_id":  "span-fallback",
	}
	span := s.recordToOTelSpan(record)
	assert.Equal(t, "trace-fallback", span.TraceID)
	assert.Equal(t, "span-fallback", span.SpanID)
}

func TestRecordToOTelSpan_DurationAsString(t *testing.T) {
	s := &DynatraceTraceSource{}
	record := map[string]any{
		"span.id":  "s1",
		"duration": "9876543",
	}
	span := s.recordToOTelSpan(record)
	assert.Equal(t, int64(9876543), span.DurationNs)
}

func TestRecordToOTelSpan_DurationAsFloat64(t *testing.T) {
	s := &DynatraceTraceSource{}
	record := map[string]any{
		"span.id":  "s1",
		"duration": float64(5000000),
	}
	span := s.recordToOTelSpan(record)
	assert.Equal(t, int64(5000000), span.DurationNs)
}

func TestRecordToOTelSpan_NilDuration(t *testing.T) {
	s := &DynatraceTraceSource{}
	record := map[string]any{"span.id": "s1"}
	span := s.recordToOTelSpan(record)
	assert.Equal(t, int64(0), span.DurationNs)
}

func TestRecordToOTelSpan_TimestampFromStartTime(t *testing.T) {
	s := &DynatraceTraceSource{}
	record := map[string]any{
		"span.id":    "s1",
		"start_time": "2024-06-15T12:00:00Z",
	}
	span := s.recordToOTelSpan(record)
	assert.Equal(t, "2024-06-15T12:00:00Z", span.Timestamp)
}

func TestRecordToOTelSpan_SpanAttributes(t *testing.T) {
	s := &DynatraceTraceSource{}
	record := map[string]any{
		"span.id":       "s1",
		"k8s.namespace": "production",
		"http.method":   "GET",
	}
	span := s.recordToOTelSpan(record)
	assert.Equal(t, "production", span.SpanAttributes["k8s.namespace"])
	assert.Equal(t, "GET", span.SpanAttributes["http.method"])
}

func TestRecordToOTelSpan_NonStringFieldsExcludedFromAttributes(t *testing.T) {
	s := &DynatraceTraceSource{}
	record := map[string]any{
		"span.id":      "s1",
		"count":        int64(42),
		"string-field": "included",
	}
	span := s.recordToOTelSpan(record)
	_, hasCount := span.SpanAttributes["count"]
	assert.False(t, hasCount, "non-string fields should not be in SpanAttributes")
	assert.Equal(t, "included", span.SpanAttributes["string-field"])
}

// ---- convertSpanRecords ----

func TestConvertSpanRecords_Nil(t *testing.T) {
	s := &DynatraceTraceSource{}
	assert.Empty(t, s.convertSpanRecords(nil))
}

func TestConvertSpanRecords_FiltersEmptySpanID(t *testing.T) {
	s := &DynatraceTraceSource{}
	records := []map[string]any{
		{"span.id": "valid-span"},
		{"trace.id": "no-span-id"}, // no span.id → filtered out
	}
	result := s.convertSpanRecords(records)
	require.Len(t, result, 1)
	assert.Equal(t, "valid-span", result[0].SpanID)
}

func TestConvertSpanRecords_AllValid(t *testing.T) {
	s := &DynatraceTraceSource{}
	records := []map[string]any{
		{"span.id": "s1", "trace.id": "t1"},
		{"span.id": "s2", "trace.id": "t1"},
	}
	result := s.convertSpanRecords(records)
	assert.Len(t, result, 2)
}

// ---- GetLabelMapping ----

func TestDynatraceTraceSource_GetLabelMapping(t *testing.T) {
	s := &DynatraceTraceSource{}
	m := s.GetLabelMapping()
	assert.Equal(t, "k8s.workload.name", m["workload_name"])
	assert.Equal(t, "k8s.namespace.name", m["workload_namespace"])
	assert.Equal(t, "trace.id", m["trace_id"])
	assert.Equal(t, "span.id", m["span_id"])
	assert.Equal(t, "span.parent_id", m["parent_id"])
}

// ---- CountTraces ----

func TestDynatraceTraceSource_GetQuery(t *testing.T) {
	s := &DynatraceTraceSource{}
	q, err := s.GetQuery(nil, TracesV3Request{Query: "some DQL filter"})
	assert.NoError(t, err)
	assert.Equal(t, "some DQL filter", q)
}

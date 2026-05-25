package observability

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/services/query"
)

// ---- buildLogDQL ----

func TestBuildLogDQL_NoFilter(t *testing.T) {
	s := &DynatraceLogSource{}
	dql, err := s.buildLogDQL(FetchLogRequest{}, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 100)
	require.NoError(t, err)
	assert.Contains(t, dql, "fetch logs")
	assert.NotContains(t, dql, "filter")
}

func TestBuildLogDQL_WithFilter(t *testing.T) {
	s := &DynatraceLogSource{}
	dql, err := s.buildLogDQL(FetchLogRequest{Query: `status == "ERROR"`}, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 100)
	require.NoError(t, err)
	assert.True(t, strings.Contains(dql, `| filter status == "ERROR"`))
}

func TestBuildLogDQL_LimitApplied(t *testing.T) {
	s := &DynatraceLogSource{}
	dql, err := s.buildLogDQL(FetchLogRequest{}, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 42)
	require.NoError(t, err)
	assert.Contains(t, dql, "| limit 42")
}

func TestBuildLogDQL_SortDesc(t *testing.T) {
	s := &DynatraceLogSource{}
	dql, err := s.buildLogDQL(FetchLogRequest{}, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 100)
	require.NoError(t, err)
	assert.Contains(t, dql, "| sort timestamp desc")
}

func TestBuildLogDQL_FromToPresent(t *testing.T) {
	s := &DynatraceLogSource{}
	from := "2024-01-01T00:00:00Z"
	to := "2024-01-01T01:00:00Z"
	dql, err := s.buildLogDQL(FetchLogRequest{}, from, to, 100)
	require.NoError(t, err)
	assert.Contains(t, dql, from)
	assert.Contains(t, dql, to)
}

func TestBuildLogDQL_FilterBeforeSort(t *testing.T) {
	// Filter clause must come before sort in DQL
	s := &DynatraceLogSource{}
	dql, err := s.buildLogDQL(FetchLogRequest{Query: `k8s.namespace.name == "prod"`}, "f", "t", 10)
	require.NoError(t, err)
	filterIdx := strings.Index(dql, "| filter")
	sortIdx := strings.Index(dql, "| sort")
	assert.Greater(t, sortIdx, filterIdx, "sort must come after filter")
}

// ---- getTimeRange (logs) ----

func TestLogGetTimeRange_BothZero(t *testing.T) {
	s := &DynatraceLogSource{}
	before := time.Now().Add(-61 * time.Minute)
	from, to := s.getTimeRange(0, 0)
	assert.NotEmpty(t, from)
	assert.NotEmpty(t, to)

	fromTime, err := time.Parse(time.RFC3339, from)
	require.NoError(t, err)
	assert.True(t, fromTime.After(before), "from should be roughly 1 hour ago")
}

func TestLogGetTimeRange_RFC3339Format(t *testing.T) {
	s := &DynatraceLogSource{}
	from, to := s.getTimeRange(0, 0)
	_, errFrom := time.Parse(time.RFC3339, from)
	_, errTo := time.Parse(time.RFC3339, to)
	assert.NoError(t, errFrom, "from must be valid RFC3339")
	assert.NoError(t, errTo, "to must be valid RFC3339")
}

func TestLogGetTimeRange_MillisInput(t *testing.T) {
	s := &DynatraceLogSource{}
	// Epoch ms for 2024-01-01T00:00:00Z
	startMs := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	endMs := time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC).UnixMilli()
	from, to := s.getTimeRange(startMs, endMs)
	assert.Contains(t, from, "2024-01-01")
	assert.Contains(t, to, "2024-01-01")
}

func TestLogGetTimeRange_SecondsInputNormalized(t *testing.T) {
	s := &DynatraceLogSource{}
	// Value < 1e12 is treated as seconds and multiplied ×1000
	startSec := int64(1_000_000_000) // 2001-09-09 as epoch seconds
	from, _ := s.getTimeRange(startSec, startSec+3600)
	assert.NotEmpty(t, from)
	_, err := time.Parse(time.RFC3339, from)
	assert.NoError(t, err)
}

func TestLogGetTimeRange_FromBeforeTo(t *testing.T) {
	s := &DynatraceLogSource{}
	startMs := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	endMs := time.Date(2024, 6, 1, 2, 0, 0, 0, time.UTC).UnixMilli()
	from, to := s.getTimeRange(startMs, endMs)

	fromTime, _ := time.Parse(time.RFC3339, from)
	toTime, _ := time.Parse(time.RFC3339, to)
	assert.True(t, fromTime.Before(toTime), "from must be before to")
}

// ---- convertToOutputLogs ----

func TestConvertToOutputLogs_Nil(t *testing.T) {
	s := &DynatraceLogSource{}
	result := s.convertToOutputLogs(nil)
	assert.Empty(t, result)
}

func TestConvertToOutputLogs_Empty(t *testing.T) {
	s := &DynatraceLogSource{}
	result := s.convertToOutputLogs([]map[string]any{})
	assert.Empty(t, result)
}

func TestConvertToOutputLogs_FieldMapping(t *testing.T) {
	s := &DynatraceLogSource{}
	records := []map[string]any{
		{
			"timestamp": "2024-01-01T00:00:00Z",
			"content":   "hello world",
		},
	}
	logs := s.convertToOutputLogs(records)
	require.Len(t, logs, 1)
	assert.Equal(t, "2024-01-01T00:00:00Z", logs[0].Timestamp)
	assert.Equal(t, "hello world", logs[0].Message)
}

func TestConvertToOutputLogs_SeverityFromStatus(t *testing.T) {
	s := &DynatraceLogSource{}
	// "status" should take priority over "loglevel"
	records := []map[string]any{
		{"status": "ERROR", "loglevel": "WARN"},
	}
	logs := s.convertToOutputLogs(records)
	require.Len(t, logs, 1)
	assert.Equal(t, "ERROR", logs[0].Severity, "status field should take priority")
}

func TestConvertToOutputLogs_SeverityFallbackToLoglevel(t *testing.T) {
	s := &DynatraceLogSource{}
	records := []map[string]any{
		{"loglevel": "WARN"},
	}
	logs := s.convertToOutputLogs(records)
	require.Len(t, logs, 1)
	assert.Equal(t, "WARN", logs[0].Severity)
}

func TestConvertToOutputLogs_NoSeverity(t *testing.T) {
	s := &DynatraceLogSource{}
	records := []map[string]any{
		{"content": "no severity fields"},
	}
	logs := s.convertToOutputLogs(records)
	require.Len(t, logs, 1)
	assert.Equal(t, "", logs[0].Severity)
}

func TestConvertToOutputLogs_LabelsFlattened(t *testing.T) {
	s := &DynatraceLogSource{}
	records := []map[string]any{
		{
			"k8s.namespace.name": "production",
			"k8s.pod.name":       "my-pod-abc",
			"content":            "test message",
		},
	}
	logs := s.convertToOutputLogs(records)
	require.Len(t, logs, 1)
	assert.Equal(t, "production", logs[0].Labels["k8s.namespace.name"])
	assert.Equal(t, "my-pod-abc", logs[0].Labels["k8s.pod.name"])
}

func TestConvertToOutputLogs_NonStringLabels(t *testing.T) {
	// Non-string values in the record should still be included in Labels (as any)
	s := &DynatraceLogSource{}
	records := []map[string]any{
		{"count": int64(42), "content": "msg"},
	}
	logs := s.convertToOutputLogs(records)
	require.Len(t, logs, 1)
	assert.Equal(t, int64(42), logs[0].Labels["count"])
}

func TestConvertToOutputLogs_MultipleRecords(t *testing.T) {
	s := &DynatraceLogSource{}
	records := []map[string]any{
		{"content": "first"},
		{"content": "second"},
		{"content": "third"},
	}
	logs := s.convertToOutputLogs(records)
	assert.Len(t, logs, 3)
}

// ---- GetLabelMapping ----

func TestDynatraceLogSource_GetLabelMapping(t *testing.T) {
	s := &DynatraceLogSource{}
	m := s.GetLabelMapping()
	assert.Equal(t, "content", m["message"])
	assert.Equal(t, "status", m["severity"])
	assert.Equal(t, "host.name", m["host"])
	assert.Equal(t, "k8s.namespace.name", m["namespace"])
	assert.Equal(t, "k8s.pod.name", m["pod"])
	assert.Equal(t, "k8s.container.name", m["container"])
}

// ---- QueryLabels ----

func TestDynatraceLogSource_QueryLabels(t *testing.T) {
	// With no account_id the config lookup fails and we fall back to the static list.
	s := &DynatraceLogSource{}
	labels, err := s.QueryLabels(nil, FetchLogLabelRequest{})
	assert.NoError(t, err)
	assert.Len(t, labels, 7)

	labelNames := make([]string, len(labels))
	for i, l := range labels {
		labelNames[i] = l.Label
	}
	assert.Contains(t, labelNames, "status")
	assert.Contains(t, labelNames, "k8s.namespace.name")
	assert.Contains(t, labelNames, "k8s.pod.name")
	assert.Contains(t, labelNames, "k8s.cluster.name")
}

func TestFetchDynatraceLogLabelsViaAutocompleteIntegration(t *testing.T) {
	baseURL := os.Getenv("DT_BASE_URL")
	apiToken := os.Getenv("DT_TOKEN")
	if baseURL == "" || apiToken == "" {
		t.Skip("DT_BASE_URL and DT_TOKEN env vars required for Dynatrace integration tests")
	}

	labels, err := fetchDynatraceLogLabelsViaAutocomplete(baseURL, apiToken)
	require.NoError(t, err)
	assert.Greater(t, len(labels), 7, "autocomplete should return more than the 7 static labels")

	labelNames := make([]string, len(labels))
	for i, l := range labels {
		labelNames[i] = l.Label
	}
	assert.Contains(t, labelNames, "status")
	assert.Contains(t, labelNames, "k8s.namespace.name")
	assert.Contains(t, labelNames, "content")
}

// ---- GetQuery ----

func TestDynatraceLogSource_GetQuery(t *testing.T) {
	s := &DynatraceLogSource{}
	q, err := s.GetQuery(nil, FetchLogRequest{Query: `status == "ERROR"`})
	assert.NoError(t, err)
	// GetQuery now returns a full DQL statement; the raw filter must be embedded in it.
	assert.Contains(t, q, `| filter status == "ERROR"`)
	assert.Contains(t, q, "fetch logs")
}

func TestDynatraceLogSource_GetQuery_Empty(t *testing.T) {
	s := &DynatraceLogSource{}
	q, err := s.GetQuery(nil, FetchLogRequest{})
	assert.NoError(t, err)
	// No filter — the DQL should still be a valid fetch statement without a filter clause.
	assert.Contains(t, q, "fetch logs")
	assert.NotContains(t, q, "| filter")
}

// ---- buildLogDQL with QueryRequest ----

func TestBuildLogDQL_FromQueryRequest(t *testing.T) {
	s := &DynatraceLogSource{}
	req := FetchLogRequest{
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"status": {query.Eq: "ERROR"},
				},
			},
		},
	}
	dql, err := s.buildLogDQL(req, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 100)
	require.NoError(t, err)
	assert.Contains(t, dql, `| filter status == "ERROR"`)
}

func TestBuildLogDQL_QueryTakesPrecedence(t *testing.T) {
	s := &DynatraceLogSource{}
	req := FetchLogRequest{
		Query: `status == "WARN"`,
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"status": {query.Eq: "ERROR"},
				},
			},
		},
	}
	dql, err := s.buildLogDQL(req, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 100)
	require.NoError(t, err)
	assert.Contains(t, dql, `| filter status == "WARN"`)
	assert.NotContains(t, dql, "ERROR")
}

func TestBuildLogDQL_ReturnsError_BadOperator(t *testing.T) {
	s := &DynatraceLogSource{}
	req := FetchLogRequest{
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"field": {query.HasKey: "x"},
				},
			},
		},
	}
	_, err := s.buildLogDQL(req, "f", "t", 100)
	assert.Error(t, err)
}

// ---- buildDQLWhereClause ----

func TestBuildDQLWhereClause_Empty(t *testing.T) {
	result, err := buildDQLWhereClause(query.QueryWhereClause{})
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestBuildDQLWhereClause_Eq(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"status": {query.Eq: "ERROR"},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `status == "ERROR"`, result)
}

func TestBuildDQLWhereClause_Neq(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"status": {query.Nq: "INFO"},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `status != "INFO"`, result)
}

func TestBuildDQLWhereClause_NumericEq(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"count": {query.Eq: int64(42)},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, "count == 42", result)
}

func TestBuildDQLWhereClause_In(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"status": {query.In: []any{"ERROR", "WARN"}},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `in(status, "ERROR", "WARN")`, result)
}

func TestBuildDQLWhereClause_In_Single(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"status": {query.In: []any{"ERROR"}},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `status == "ERROR"`, result)
}

func TestBuildDQLWhereClause_NotIn(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"status": {query.NotIn: []any{"INFO", "DEBUG"}},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `not(in(status, "INFO", "DEBUG"))`, result)
}

func TestBuildDQLWhereClause_And(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"status": {query.Eq: "ERROR"}}},
			{Binary: query.BinaryWhereClause{"k8s.namespace.name": {query.Eq: "prod"}}},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Contains(t, result, `status == "ERROR"`)
	assert.Contains(t, result, `k8s.namespace.name == "prod"`)
	assert.Contains(t, result, " AND ")
}

func TestBuildDQLWhereClause_Or(t *testing.T) {
	where := query.QueryWhereClause{
		Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"status": {query.Eq: "ERROR"}}},
			{Binary: query.BinaryWhereClause{"status": {query.Eq: "WARN"}}},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Contains(t, result, `status == "ERROR"`)
	assert.Contains(t, result, `status == "WARN"`)
	assert.Contains(t, result, " OR ")
}

func TestBuildDQLWhereClause_Not(t *testing.T) {
	inner := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"status": {query.Eq: "INFO"}},
	}
	where := query.QueryWhereClause{Not: &inner}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `not(status == "INFO")`, result)
}

func TestBuildDQLWhereClause_Like(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"content": {query.Like: "%exception%"},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	// % wildcards stripped; DQL contains() used
	assert.Equal(t, `contains(content, "exception")`, result)
}

func TestBuildDQLWhereClause_ILike(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"content": {query.ILike: "%error%"},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `matchesPhrase(content, "error")`, result)
}

func TestBuildDQLWhereClause_Contains(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"content": {query.Contains: "timeout"},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `contains(content, "timeout")`, result)
}

func TestBuildDQLWhereClause_FieldMapping(t *testing.T) {
	// "severity" is a standard NudgeBee name → should map to Dynatrace "status"
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"severity": {query.Eq: "ERROR"},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `status == "ERROR"`, result)
	assert.NotContains(t, result, "severity")
}

// ---- GetQuery with QueryRequest ----

func TestGetQuery_FullDQLFromQueryRequest(t *testing.T) {
	s := &DynatraceLogSource{}
	req := FetchLogRequest{
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"status": {query.Eq: "ERROR"},
				},
			},
		},
	}
	q, err := s.GetQuery(nil, req)
	require.NoError(t, err)
	assert.Contains(t, q, "fetch logs")
	assert.Contains(t, q, `| filter status == "ERROR"`)
	assert.Contains(t, q, "| sort timestamp desc")
	assert.Contains(t, q, "| limit")
}

func TestGetQuery_RawQueryInFullDQL(t *testing.T) {
	s := &DynatraceLogSource{}
	req := FetchLogRequest{Query: `k8s.namespace.name == "prod"`}
	q, err := s.GetQuery(nil, req)
	require.NoError(t, err)
	assert.Contains(t, q, `| filter k8s.namespace.name == "prod"`)
}

func TestGetQuery_NoFilter_NoFilterClause(t *testing.T) {
	s := &DynatraceLogSource{}
	q, err := s.GetQuery(nil, FetchLogRequest{})
	require.NoError(t, err)
	assert.Contains(t, q, "fetch logs")
	assert.NotContains(t, q, "| filter")
}

func TestBuildDQLWhereClause_StringNumericEq(t *testing.T) {
	// When the value arrives as a JSON string (e.g. "200") but the Dynatrace field
	// is numeric (e.g. http.response.status_code), it must be rendered without quotes
	// so Dynatrace matches it against the stored integer value.
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"http.response.status_code": {query.Eq: "200"},
		},
	}
	result, err := buildDQLWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, "http.response.status_code == 200", result)
}

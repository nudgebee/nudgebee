package observability

import (
	"os"
	"testing"
	"time"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfNoDTCreds skips the test when DT_TOKEN or DT_BASE_URL are not set.
func skipIfNoDTCreds(t *testing.T) (token, baseURL string) {
	t.Helper()
	token = os.Getenv("DT_TOKEN")
	baseURL = os.Getenv("DT_BASE_URL")
	if token == "" || baseURL == "" {
		t.Skip("DT_TOKEN and DT_BASE_URL env vars required for Dynatrace integration tests")
	}
	return token, baseURL
}

// dtTimeRange returns from/to strings covering the past hour in RFC3339 format.
func dtTimeRange() (from, to string) {
	now := time.Now().UTC()
	return now.Add(-1 * time.Hour).Format(time.RFC3339), now.Format(time.RFC3339)
}

// ---- Connectivity ----

// TestDynatrace_Integration_Connectivity verifies that the Grail API is reachable
// and responds with a valid result for a minimal DQL query.
func TestDynatrace_Integration_Connectivity(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	result, err := executeDQLQuery(baseURL, token, "fetch logs | limit 1")
	require.NoError(t, err, "should connect to Grail successfully")
	require.NotNil(t, result, "result must not be nil")
	t.Logf("connectivity check: %d record(s) returned", len(result.Records))
}

// ---- Logs ----

// TestDynatrace_Integration_FetchLogs fetches the 10 most recent logs.
func TestDynatrace_Integration_FetchLogs(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	s := &DynatraceLogSource{}
	from, to := dtTimeRange()
	dql, err := s.buildLogDQL(FetchLogRequest{}, from, to, 10)
	require.NoError(t, err)
	t.Logf("DQL: %s", dql)

	result, err := executeDQLQuery(baseURL, token, dql)
	require.NoError(t, err)
	require.NotNil(t, result)

	logs := s.convertToOutputLogs(result.Records)
	t.Logf("Retrieved %d log(s)", len(logs))
	// Just verify the conversion produces valid OutputLog structs (no panics).
	for _, log := range logs {
		assert.NotEmpty(t, log.Timestamp, "each log entry should have a timestamp")
	}
}

// TestDynatrace_Integration_FetchLogsWithFilter fetches ERROR-level logs.
// The result may be empty if there are no recent errors.
func TestDynatrace_Integration_FetchLogsWithFilter(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	s := &DynatraceLogSource{}
	from, to := dtTimeRange()
	dql, err := s.buildLogDQL(FetchLogRequest{Query: `status == "ERROR"`}, from, to, 5)
	require.NoError(t, err)
	t.Logf("DQL: %s", dql)

	result, err := executeDQLQuery(baseURL, token, dql)
	require.NoError(t, err, "filtered log query should not error even with zero results")
	require.NotNil(t, result)

	logs := s.convertToOutputLogs(result.Records)
	t.Logf("Retrieved %d ERROR log(s)", len(logs))
	for _, log := range logs {
		assert.Equal(t, "ERROR", log.Severity, "severity should be ERROR")
	}
}

// TestDynatrace_Integration_FetchLogsQueryRequest validates the bug fix: structured
// QueryRequest.Where conditions are correctly translated to a DQL filter and executed.
func TestDynatrace_Integration_FetchLogsQueryRequest(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	s := &DynatraceLogSource{}
	from, to := dtTimeRange()

	// Use QueryRequest.Where (no raw Query) — this is the path that was previously broken.
	req := FetchLogRequest{
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					// "severity" is the standard NudgeBee name; must map to "status" in DQL.
					"severity": {query.Eq: "ERROR"},
				},
			},
		},
	}

	dql, err := s.buildLogDQL(req, from, to, 5)
	require.NoError(t, err)
	t.Logf("Generated DQL: %s", dql)

	// Confirm the DQL contains the mapped field name, not the original.
	assert.Contains(t, dql, `status == "ERROR"`, "label mapping must translate severity→status")
	assert.NotContains(t, dql, "severity", "original NudgeBee field name must not appear in DQL")

	result, err := executeDQLQuery(baseURL, token, dql)
	require.NoError(t, err, "DQL generated from QueryRequest.Where must execute successfully")
	require.NotNil(t, result)

	logs := s.convertToOutputLogs(result.Records)
	t.Logf("Retrieved %d ERROR log(s) via QueryRequest.Where", len(logs))
	for _, log := range logs {
		assert.Equal(t, "ERROR", log.Severity)
	}
}

// TestDynatrace_Integration_LogLabelValues samples recent logs and returns
// distinct values for the "status" label.
func TestDynatrace_Integration_LogLabelValues(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	from, to := dtTimeRange()
	dql := `fetch logs, from: "` + from + `", to: "` + to + `" | sort timestamp desc | limit 200`

	result, err := executeDQLQuery(baseURL, token, dql)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Manually collect "status" label values (mirrors DynatraceLogSource.QueryLabelValues logic).
	seen := make(map[string]bool)
	var values []string
	for _, record := range result.Records {
		val := grailStr(record, "status")
		if val != "" && !seen[val] {
			seen[val] = true
			values = append(values, val)
		}
	}
	t.Logf("Distinct status values: %v", values)
	// No hard assert on values — the set depends on what the tenant has.
}

// ---- Spans / Traces ----

// TestDynatrace_Integration_FetchSpans fetches up to 10 recent spans.
func TestDynatrace_Integration_FetchSpans(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	s := &DynatraceTraceSource{}
	from, to := dtTimeRange()
	dql := `fetch spans, from: "` + from + `", to: "` + to + `" | sort start_time desc | limit 10`
	t.Logf("DQL: %s", dql)

	result, err := executeDQLQuery(baseURL, token, dql)
	require.NoError(t, err)
	require.NotNil(t, result)

	spans := s.convertSpanRecords(result.Records)
	t.Logf("Retrieved %d span(s)", len(spans))
	for _, span := range spans {
		assert.NotEmpty(t, span.SpanID, "each span should have a SpanID")
	}
}

// TestDynatrace_Integration_FetchSpansForTraceID fetches spans for a specific trace.
// It first gets a recent span, then re-fetches all spans sharing that trace ID.
func TestDynatrace_Integration_FetchSpansForTraceID(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	from, to := dtTimeRange()

	// Step 1: get any recent span to grab a trace ID.
	seedDQL := `fetch spans, from: "` + from + `", to: "` + to + `" | sort start_time desc | limit 5`
	seedResult, err := executeDQLQuery(baseURL, token, seedDQL)
	require.NoError(t, err)
	require.NotNil(t, seedResult)

	if len(seedResult.Records) == 0 {
		t.Skip("no spans in the past hour — skipping trace ID fetch")
	}

	traceID := grailStr(seedResult.Records[0], "trace.id")
	if traceID == "" {
		t.Skip("seed span had no trace.id — skipping")
	}
	t.Logf("Fetching spans for trace.id=%s", traceID)

	// Step 2: fetch all spans for that trace ID.
	traceDQL := `fetch spans, from: "` + from + `", to: "` + to + `" | filter trace.id == "` + traceID + `" | sort start_time asc`
	traceResult, err := executeDQLQuery(baseURL, token, traceDQL)
	require.NoError(t, err)
	require.NotNil(t, traceResult)

	s := &DynatraceTraceSource{}
	spans := s.convertSpanRecords(traceResult.Records)
	t.Logf("Trace %s has %d span(s)", traceID, len(spans))

	for _, span := range spans {
		assert.Equal(t, traceID, span.TraceID, "all spans must share the queried trace ID")
	}
}

// TestDynatrace_Integration_SpanLabelValues samples recent spans and logs
// distinct workload names (k8s.workload.name).
func TestDynatrace_Integration_SpanLabelValues(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	from, to := dtTimeRange()
	dql := `fetch spans, from: "` + from + `", to: "` + to + `" | sort start_time desc | limit 200`

	result, err := executeDQLQuery(baseURL, token, dql)
	require.NoError(t, err)
	require.NotNil(t, result)

	s := &DynatraceTraceSource{}
	values := s.collectLabelValues(result.Records, "k8s.workload.name", 20)
	t.Logf("Distinct k8s.workload.name values (up to 20): %v", values)
}

// TestDynatrace_Integration_GroupedTraces calls the server-side DQL grouped aggregation pipeline.
func TestDynatrace_Integration_GroupedTraces(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	from, to := dtTimeRange()
	s := &DynatraceTraceSource{}
	req := TracesV3Request{}
	dql, err := s.buildGroupedSpansDQL(req, from, to)
	require.NoError(t, err)

	result, err := executeDQLQuery(baseURL, token, dql)
	require.NoError(t, err)
	require.NotNil(t, result)

	groups := s.convertDQLGroupedToTraceGroupingValues(result.Records)
	t.Logf("DQL returned %d record(s) → %d group(s)", len(result.Records), len(groups))

	for _, g := range groups {
		assert.GreaterOrEqual(t, g.Count, 1, "each group must have at least one span")
		assert.GreaterOrEqual(t, g.MaxLatency, int64(0), "max latency must be non-negative")
		assert.GreaterOrEqual(t, g.ErrorCount, 0, "error count must be non-negative")
		assert.LessOrEqual(t, g.ErrorCount, g.Count, "error count must not exceed total count")
	}
}

// ---- Metrics ----

// TestDynatrace_Integration_FetchMetrics queries a Dynatrace built-in CPU metric
// for the past hour and verifies the timeseries structure.
func TestDynatrace_Integration_FetchMetrics(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	s := &DynatraceMetricSource{}
	from, to := dtTimeRange()
	dql, err := s.buildMetricDQL("dt.host.cpu.usage", from, to, "5m", nil, nil)
	require.NoError(t, err)
	t.Logf("DQL: %s", dql)

	result, err := executeDQLQuery(baseURL, token, dql)
	require.NoError(t, err)
	require.NotNil(t, result)

	t.Logf("Retrieved %d metric record(s)", len(result.Records))

	if len(result.Records) > 0 {
		timestamps := s.extractTimestamps(result)
		t.Logf("Timestamps: %d point(s)", len(timestamps))

		values := s.extractValues(result.Records[0])
		t.Logf("Values in first record: %d", len(values))

		// If metadata is present, timestamps and values should align.
		if len(timestamps) > 0 && len(values) > 0 {
			assert.Equal(t, len(timestamps), len(values),
				"number of timestamps should equal number of values")
		}
	}
}

// TestDynatrace_Integration_MetricPassthroughDQL verifies that a manually
// composed timeseries DQL query passes through and executes correctly.
func TestDynatrace_Integration_MetricPassthroughDQL(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	from, to := dtTimeRange()
	rawDQL := `timeseries val = avg(dt.host.cpu.usage), from: "` + from + `", to: "` + to + `", interval: 5m`
	t.Logf("DQL: %s", rawDQL)

	result, err := executeDQLQuery(baseURL, token, rawDQL)
	require.NoError(t, err, "passthrough timeseries DQL should execute without error")
	require.NotNil(t, result)
	t.Logf("Retrieved %d metric record(s)", len(result.Records))
}

// ---- Metric Labels ----

// TestDynatrace_Integration_FetchMetricsLabels fetches dimension keys for
// builtin:host.cpu.usage via the DQL autocomplete endpoint (platform token compatible).
func TestDynatrace_Integration_FetchMetricsLabels(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	output, err := fetchDTMetricDimensionsViaAutocomplete(baseURL, token, "builtin:host.cpu.usage")
	require.NoError(t, err)

	t.Logf("Dimensions: %d", len(output))
	for _, dim := range output {
		t.Logf("  label=%s", dim.Label)
		assert.NotEmpty(t, dim.Label, "each dimension must have a non-empty label")
	}
}

// TestDynatrace_Integration_FetchMetricLabelValues queries recent timeseries data for
// dt.host.cpu.usage and extracts distinct values for the dt.entity.host dimension.
func TestDynatrace_Integration_FetchMetricLabelValues(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	from, to := dtTimeRange()
	output, err := fetchDTMetricLabelValues(baseURL, token, "dt.host.cpu.usage", "dt.entity.host", from, to)
	require.NoError(t, err)
	t.Logf("Distinct dt.entity.host values: %d", len(output))
	assert.NotEmpty(t, output, "expected at least one host entity value")
	for _, v := range output {
		assert.NotEmpty(t, v.Value, "each label value must be non-empty")
		assert.NotNil(t, v.Attributes)
	}
}

// TestDynatrace_Integration_FetchMetricLabelValues_LabelNotPresent verifies that
// grouping by a dimension with no data returns an empty slice (not an error).
func TestDynatrace_Integration_FetchMetricLabelValues_LabelNotPresent(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	from, to := dtTimeRange()
	// "does.not.exist" is not a real dimension — Dynatrace may return 0 records or an error.
	// Either outcome is acceptable; what must NOT happen is a panic or unexpected behaviour.
	output, err := fetchDTMetricLabelValues(baseURL, token, "dt.host.cpu.usage", "does.not.exist", from, to)
	if err != nil {
		t.Logf("DQL returned error for unknown dimension (acceptable): %v", err)
		return
	}
	assert.Empty(t, output, "unknown label key should yield no values")
}

// ---- Error cases ----

// TestDynatrace_Integration_InvalidDQL verifies that a syntactically invalid DQL
// query returns a descriptive error.
func TestDynatrace_Integration_InvalidDQL(t *testing.T) {
	token, baseURL := skipIfNoDTCreds(t)

	_, err := executeDQLQuery(baseURL, token, "NOT VALID DQL SYNTAX ;;;")
	require.Error(t, err, "invalid DQL should return an error")
	t.Logf("Error for invalid DQL: %v", err)
}

// TestDynatrace_Integration_InvalidToken verifies that a wrong token causes an
// authentication error.
func TestDynatrace_Integration_InvalidToken(t *testing.T) {
	_, baseURL := skipIfNoDTCreds(t)

	_, err := executeDQLQuery(baseURL, "dt0s01.INVALIDTOKEN.XXXX", "fetch logs | limit 1")
	require.Error(t, err, "bad token should return an error")
	t.Logf("Error for invalid token: %v", err)
}

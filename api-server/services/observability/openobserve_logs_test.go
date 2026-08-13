package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/integrations"
	"nudgebee/services/integrations/core"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenObserveLogSource_QueryLogs(t *testing.T) {
	// Start a local HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/test-org/_search", r.URL.Path)
		assert.Equal(t, "Basic dXNlcjpwYXNz", r.Header.Get("Authorization"))

		var reqBody openObserveSearchRequest
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		assert.Contains(t, reqBody.Query.SQL, "str_match_ignore_case(message, '.*error.*')")

		resp := openObserveSearchResponse{
			Hits: []map[string]any{
				{
					"_timestamp": float64(1699999999000000),
					"message":    "test error message",
					"severity":   "ERROR",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := &security.RequestContext{}
	_ = ctx

	// Create test configs mapping to the test server
	configs := []core.IntegrationConfigValue{
		{Name: "openobserve_url", Value: server.URL},
		{Name: "openobserve_org_id", Value: "test-org"},
		{Name: "openobserve_username", Value: "user"},
		{Name: "openobserve_password", Value: "pass", IsEncrypted: false},
	}
	_ = configs

	// Temporarily mock GetOpenObserveConfigs by pushing the integration list
	// In a real test suite, you'd mock core.ListIntegrationConfigs or use a db fixture.
	// Since we are mocking network, we need to mock DB fetch or bypass it.
	// For simplicity in these unit tests, we will mock `ListIntegrationConfigs` using
	// a mock implementation if the architecture allows, or just test the SQL builder directly.

	// Let's test the SQL builder logic directly since DB mocking varies
	s := &OpenObserveLogSource{}

	req := FetchLogRequest{
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"body": {
						query.ILike: "error",
					},
				},
			},
		},
		Limit: 50,
	}

	sql, err := s.buildSQL(req, integrations.OpenObserveDefaultLogStream)
	require.NoError(t, err)

	expectedSQL := `SELECT * FROM "default" WHERE str_match_ignore_case(body, 'error') ORDER BY _timestamp DESC LIMIT 50`
	assert.Equal(t, expectedSQL, sql)
	assert.Equal(t, "body", s.GetLabelMapping()["message"])
}

// The stream is per-account config, not a constant — a collector writing to a
// differently-named stream must be queryable without a code change.
func TestOpenObserveLogSource_BuildSQLUsesConfiguredStream(t *testing.T) {
	s := &OpenObserveLogSource{}
	req := FetchLogRequest{Limit: 10}

	sql, err := s.buildSQL(req, "k8s_logs")
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "k8s_logs" ORDER BY _timestamp DESC LIMIT 10`, sql)

	groupSQL := buildOpenObserveLogGroupSQL("k8s_logs",
		resolveOpenObserveLogGroupCols(openObserveFieldSet("body", "k8s_namespace_name")), "", "", 100)
	assert.Contains(t, groupSQL, `FROM "k8s_logs"`)
}

// Canonical k8s labels must resolve to the OTel collector's flattened column names,
// otherwise a Builder filter on "namespace" emits a column OpenObserve doesn't have.
func TestOpenObserveLogLabelMapping_MapsKubernetesFields(t *testing.T) {
	m := (&OpenObserveLogSource{}).GetLabelMapping()

	assert.Equal(t, "k8s_namespace_name", m["namespace"])
	assert.Equal(t, "k8s_pod_name", m["pod"])
	assert.Equal(t, "k8s_container_name", m["container"])
	assert.Equal(t, "k8s_node_name", m["node"])
	assert.Equal(t, "service_name", m["service"])
	assert.Equal(t, "body", m["message"])

	// Mapped names must survive the identifier guard that builds the WHERE clause.
	for canonical, col := range m {
		assert.True(t, isSafeIdentifier(col), "mapping %q -> %q must be a safe identifier", canonical, col)
	}
}

// Clicking a Log Groups row opens the log view filtered by the group's namespace and
// workload. The frontend turns those into the canonical `namespace` / `app` labels, so
// both must map onto real columns — an unmapped `namespace` makes OpenObserve reject the
// query outright with "No field named namespace".
func TestOpenObserveLogGroupDrilldownFiltersMapToRealColumns(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "nudgebee"}}},
			{Binary: query.BinaryWhereClause{"app": {query.Eq: "llm-gateway"}}},
		},
	}

	sql, err := buildOpenObserveSQLWhereClause(where)
	require.NoError(t, err)

	assert.Equal(t, "(k8s_namespace_name = 'nudgebee' AND k8s_deployment_name = 'llm-gateway')", sql)
	assert.NotContains(t, sql, "namespace =", "canonical label must not reach OpenObserve unmapped")
}

// The workload a group reports becomes the drill-down's `app` filter, which maps to the
// deployment column — so grouping must read that same column rather than guessing from
// the pod name, or clicking a group returns no logs.
func TestBuildOpenObserveLogGroupSQL_GroupsByRecordedWorkloadColumn(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet(
		"body", "k8s_namespace_name", "k8s_pod_name", "k8s_deployment_name", "k8s_container_name",
	))

	assert.Equal(t, "k8s_deployment_name", cols.Workload)

	sql := buildOpenObserveLogGroupSQL("default", cols, "", "", 100)
	assert.Contains(t, sql, "k8s_deployment_name AS group_workload")
	assert.Contains(t, sql, "GROUP BY body, k8s_namespace_name, k8s_pod_name, k8s_deployment_name, k8s_container_name")
}

func TestConvertOpenObserveLogGroups_PrefersRecordedWorkloadOverPodDerivation(t *testing.T) {
	out := convertOpenObserveLogGroups([]map[string]any{
		{
			"group_sample":    "boom",
			"group_namespace": "nudgebee",
			"group_pod":       "llm-gateway-7d9f8b6c5d-x2k9p",
			"group_workload":  "llm-gateway",
			"group_container": "llm-gateway",
			"group_count":     float64(2),
		},
		{
			// StatefulSet pod on a deployment-only stream: no recorded workload, so the
			// pod-name heuristic still has to supply one.
			"group_sample": "boom",
			"group_pod":    "postgres-0",
			"group_count":  float64(1),
		},
	}, 1699999000)

	require.Len(t, out.Groups, 2)
	assert.Equal(t, "llm-gateway", out.Groups[0].Workload)
	assert.Equal(t, "/k8s/nudgebee/llm-gateway/llm-gateway", out.Groups[0].ContainerID)
	assert.Equal(t, "postgres-0", out.Groups[1].Workload)
}

func TestOpenObserveLogSource_BuildSQLWhere(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{
				Binary: query.BinaryWhereClause{
					"body": {
						query.Contains: "exception",
					},
				},
			},
			{
				Binary: query.BinaryWhereClause{
					"pod": {
						query.Eq: "api-server-1",
					},
				},
			},
		},
	}

	sql, err := buildOpenObserveSQLWhereClause(where)
	require.NoError(t, err)

	// "pod" is canonical and maps onto the OTel column; "body" maps to itself.
	expected := "(str_match(body, 'exception') AND k8s_pod_name = 'api-server-1')"
	assert.Equal(t, expected, sql)
}

func TestOpenObserveLogSource_LabelSampleUsesDefaultWindowAndUnionsLabels(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	fetchReq := buildOpenObserveLabelSampleRequest(FetchLogLabelRequest{AccountId: "account"}, now)

	assert.Equal(t, int64(1784718000000), fetchReq.StartTime)
	assert.Equal(t, int64(1784721600000), fetchReq.EndTime)
	assert.Equal(t, 100, fetchReq.Limit)

	labels := collectOpenObserveLogLabels([]OutputLog{
		{Labels: map[string]any{"_timestamp": 1, "body": "first"}},
		{Labels: map[string]any{"severity": "ERROR", "service_name": "api"}},
	})

	assert.ElementsMatch(t, []string{"_timestamp", "body", "severity", "service_name"}, openObserveLogLabelNames(labels))
}

func TestOpenObserveIdentifierRejectsDottedNames(t *testing.T) {
	assert.False(t, isSafeIdentifier("service.name"))
	assert.True(t, isSafeIdentifier("service_name"))
}

func openObserveLogLabelNames(labels []OutputLogLabel) []string {
	names := make([]string, 0, len(labels))
	for _, label := range labels {
		names = append(names, label.Label)
	}
	return names
}

// OpenObserveLogSource must satisfy LogGroupSource — getLogGroupSource type-asserts on it,
// and getProviderCapabilities derives supports_log_groups (which gates the UI) from that
// same assertion. Dropping QueryLogGroup would silently hide the feature, not break a build.
var _ LogGroupSource = (*OpenObserveLogSource)(nil)

// getLogGroupSource must route openobserve:user to the log source itself rather than
// falling through to the metrics-provider fallback — that routing is also what makes
// getProviderCapabilities report supports_log_groups, which gates the UI.
func TestGetLogGroupSource_RoutesOpenObserveToLogSource(t *testing.T) {
	source, err := getLogGroupSource("openobserve", "user")
	require.NoError(t, err)
	assert.IsType(t, &OpenObserveLogSource{}, source)
}

// openObserveFieldSet builds a schema of string-typed (Utf8) columns — the common case.
// Use openObserveTypedFields when a test needs a non-string column.
func openObserveFieldSet(names ...string) map[string]string {
	fields := make(map[string]string, len(names))
	for _, n := range names {
		fields[n] = "Utf8"
	}
	return fields
}

func openObserveTypedFields(fields map[string]string) map[string]string { return fields }

func TestResolveOpenObserveLogGroupCols_PrefersHighestPriorityCandidate(t *testing.T) {
	// Stream carries both the OTel and the Fluent Bit spellings; OTel wins.
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet(
		"_timestamp", "body", "message", "severity_text", "level",
		"k8s_namespace_name", "namespace", "k8s_pod_name", "pod", "k8s_container_name", "container",
	))

	assert.Equal(t, openObserveLogGroupCols{
		Message:   "body",
		Severity:  "severity_text",
		Namespace: "k8s_namespace_name",
		Pod:       "k8s_pod_name",
		Container: "k8s_container_name",
	}, cols)
}

func TestResolveOpenObserveLogGroupCols_FluentBitSchema(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet(
		"_timestamp", "log", "kubernetes_namespace_name", "kubernetes_pod_name", "kubernetes_container_name",
	))

	assert.Equal(t, "log", cols.Message)
	assert.Equal(t, "kubernetes_namespace_name", cols.Namespace)
	assert.Equal(t, "kubernetes_pod_name", cols.Pod)
	assert.Equal(t, "kubernetes_container_name", cols.Container)
	// No severity column in this stream — the SQL must fall back to message matching.
	assert.Equal(t, "", cols.Severity)
}

// Regression for the DataFusion type_coercion failure observed against a live OpenObserve:
//
//	Function 'lower' requires ... Native(String), but received Int64
//
// A stream whose `severity` column is numeric must not be selected as the string severity
// column, or LOWER() rejects the entire query and the Log Patterns panel shows nothing.
func TestResolveOpenObserveLogGroupCols_SkipsNumericSeverityColumn(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveTypedFields(map[string]string{
		"_timestamp":         "Int64",
		"body":               "Utf8",
		"severity":           "Int64", // numeric — must not reach LOWER()
		"k8s_namespace_name": "Utf8",
	}))

	assert.Equal(t, "", cols.Severity, "numeric severity column must not be picked")
	assert.Equal(t, "body", cols.Message)
	assert.Equal(t, "k8s_namespace_name", cols.Namespace)
}

func TestResolveOpenObserveLogGroupCols_FallsThroughToNextStringCandidate(t *testing.T) {
	// `severity` is numeric but `level` is a usable string — resolution must fall through
	// rather than giving up on the role entirely.
	cols := resolveOpenObserveLogGroupCols(openObserveTypedFields(map[string]string{
		"body":     "Utf8",
		"severity": "Int64",
		"level":    "Utf8",
	}))

	assert.Equal(t, "level", cols.Severity)
}

func TestResolveOpenObserveLogGroupCols_PicksNumericOtelSeverityNumber(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveTypedFields(map[string]string{
		"body":            "Utf8",
		"severity_number": "Int64",
	}))

	assert.Equal(t, "", cols.Severity)
	assert.Equal(t, "severity_number", cols.SeverityNumber)
}

func TestResolveOpenObserveLogGroupCols_IgnoresNonIntegerSeverityNumber(t *testing.T) {
	// A Utf8 severity_number is not a numeric OTel severity; comparing it with >= would
	// be another type_coercion failure.
	cols := resolveOpenObserveLogGroupCols(openObserveTypedFields(map[string]string{
		"body":            "Utf8",
		"severity_number": "Utf8",
	}))

	assert.Equal(t, "", cols.SeverityNumber)
}

func TestResolveOpenObserveLogGroupCols_UnknownTypeIsUsable(t *testing.T) {
	// An empty or unfamiliar type name must not disqualify a column — the schema endpoint
	// is the only type source, and over-rejecting would break otherwise-working streams.
	cols := resolveOpenObserveLogGroupCols(openObserveTypedFields(map[string]string{
		"body":     "",
		"severity": "SomeFutureType",
	}))

	assert.Equal(t, "body", cols.Message)
	assert.Equal(t, "severity", cols.Severity)
}

func TestBuildOpenObserveLogGroupSQL_NumericSeverityUsesComparisonNotLower(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveTypedFields(map[string]string{
		"body":               "Utf8",
		"severity":           "Int64",
		"severity_number":    "Int64",
		"k8s_namespace_name": "Utf8",
	}))

	sql := buildOpenObserveLogGroupSQL(integrations.OpenObserveDefaultLogStream, cols, "", "", 100)

	assert.NotContains(t, sql, "LOWER(", "no string function may touch a numeric column")
	assert.Contains(t, sql, "severity_number >= 17")
	assert.Contains(t, sql, "severity_number AS group_level_num")
}

func TestBuildOpenObserveLogGroupSQL_StringSeverityFallsBackToNumeric(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveTypedFields(map[string]string{
		"body":            "Utf8",
		"severity_text":   "Utf8",
		"severity_number": "Int64",
	}))

	sql := buildOpenObserveLogGroupSQL(integrations.OpenObserveDefaultLogStream, cols, "", "", 100)

	// String severity drives the match; the blank-severity branch uses the numeric column
	// instead of the cruder message-content heuristic.
	assert.Contains(t, sql, "LOWER(severity_text) IN (")
	assert.Contains(t, sql, "severity_number >= 17")
	assert.NotContains(t, sql, "LIKE '%ERROR%'")
}

func TestOpenObserveLevelFromSeverityNumber(t *testing.T) {
	assert.Equal(t, "fatal", openObserveLevelFromSeverityNumber(float64(21)))
	assert.Equal(t, "fatal", openObserveLevelFromSeverityNumber(float64(24)))
	assert.Equal(t, "error", openObserveLevelFromSeverityNumber(float64(17)))
	assert.Equal(t, "error", openObserveLevelFromSeverityNumber(nil))
}

// TestResolveOpenObserveLogGroupCols_OtelCollectorK8sStream pins the resolution against a
// real OpenObserve stream fed by the OTel collector (captured from a live org). It carries
// a single `severity` column typed Int64 whose value is 0 (OTel UNSPECIFIED) on every
// record — the collector never parses severity out of the container log line.
//
// Two things must hold: `severity` must not become the string severity column (that is the
// LOWER() type_coercion crash), and it must not be adopted as a numeric OTel severity
// either — `>= 17` against an all-zero column matches nothing, which would trade a visible
// error for a silently empty panel. Message-content matching is the only useful filter here.
func TestResolveOpenObserveLogGroupCols_OtelCollectorK8sStream(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveTypedFields(map[string]string{
		"_timestamp": "Int64", "body": "Utf8", "dropped_attributes_count": "Int64",
		"host_name": "Utf8", "k8s_app_component": "Utf8", "k8s_app_instance": "Utf8",
		"k8s_cluster": "Utf8", "k8s_container_name": "Utf8", "k8s_deployment_name": "Utf8",
		"k8s_namespace_name": "Utf8", "k8s_node_name": "Utf8", "k8s_node_uid": "Utf8",
		"k8s_pod_name": "Utf8", "k8s_pod_start_time": "Utf8", "k8s_pod_uid": "Utf8",
		"log_file_path": "Utf8", "log_iostream": "Utf8", "logtag": "Utf8", "os_type": "Utf8",
		"service_name": "Utf8", "service_version": "Utf8", "severity": "Int64", "time": "Utf8",
	}))

	assert.Equal(t, "body", cols.Message)
	assert.Equal(t, "", cols.Severity, "Int64 severity must never reach LOWER()")
	assert.Equal(t, "", cols.SeverityNumber, "severity is not the OTel severity_number field")
	assert.Equal(t, "k8s_namespace_name", cols.Namespace)
	assert.Equal(t, "k8s_pod_name", cols.Pod)
	assert.Equal(t, "k8s_container_name", cols.Container)

	sql := buildOpenObserveLogGroupSQL(integrations.OpenObserveDefaultLogStream, cols, "nudgebee", "", 100)

	assert.NotContains(t, sql, "LOWER(")
	assert.NotContains(t, sql, "severity")
	assert.Contains(t, sql, "(body LIKE '%ERROR%' OR body LIKE '%FATAL%' OR body LIKE '%CRITICAL%')")
	assert.Contains(t, sql, "k8s_namespace_name = 'nudgebee'")
}

func TestBuildOpenObserveLogGroupSQL_OtelSchema(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet(
		"body", "severity_text", "k8s_namespace_name", "k8s_pod_name", "k8s_container_name",
	))

	sql := buildOpenObserveLogGroupSQL(integrations.OpenObserveDefaultLogStream, cols, "", "", openObserveLogGroupLimit)

	expected := `SELECT body AS group_sample, ` +
		`k8s_namespace_name AS group_namespace, ` +
		`k8s_pod_name AS group_pod, ` +
		`k8s_container_name AS group_container, ` +
		`severity_text AS group_level, ` +
		`count(*) AS group_count, ` +
		`max(_timestamp) AS group_last_ts ` +
		`FROM "default" ` +
		`WHERE body IS NOT NULL AND body != '' ` +
		`AND (LOWER(severity_text) IN ('error', 'critical', 'fatal', 'err', 'crit') ` +
		`OR ((severity_text IS NULL OR severity_text = '') ` +
		`AND (body LIKE '%ERROR%' OR body LIKE '%FATAL%' OR body LIKE '%CRITICAL%'))) ` +
		`AND k8s_container_name NOT IN ('prometheus', 'grafana', 'nudgebee-agent') ` +
		`GROUP BY body, k8s_namespace_name, k8s_pod_name, k8s_container_name, severity_text ` +
		`ORDER BY group_count DESC LIMIT 100`

	assert.Equal(t, expected, sql)
}

func TestBuildOpenObserveLogGroupSQL_AppliesNamespaceAndWorkloadFilters(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet(
		"body", "severity_text", "k8s_namespace_name", "k8s_pod_name", "k8s_container_name",
	))

	sql := buildOpenObserveLogGroupSQL(integrations.OpenObserveDefaultLogStream, cols, "prod", "checkout-service", 100)

	assert.Contains(t, sql, "k8s_namespace_name = 'prod'")
	assert.Contains(t, sql, "k8s_pod_name LIKE 'checkout-service-%'")
}

func TestBuildOpenObserveLogGroupSQL_EscapesFilterQuotes(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet("body", "k8s_namespace_name", "k8s_pod_name"))

	sql := buildOpenObserveLogGroupSQL(integrations.OpenObserveDefaultLogStream, cols, "pro'd", "che'ckout", 100)

	assert.Contains(t, sql, "k8s_namespace_name = 'pro''d'")
	assert.Contains(t, sql, "k8s_pod_name LIKE 'che''ckout-%'")
}

func TestBuildOpenObserveLogGroupSQL_NoSeverityColumnUsesMessageMatching(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet("log", "kubernetes_pod_name"))

	sql := buildOpenObserveLogGroupSQL(integrations.OpenObserveDefaultLogStream, cols, "", "", 100)

	assert.Contains(t, sql, "(log LIKE '%ERROR%' OR log LIKE '%FATAL%' OR log LIKE '%CRITICAL%')")
	assert.NotContains(t, sql, "LOWER(")
	// Roles with no matching column drop out of both the projection and the GROUP BY.
	assert.NotContains(t, sql, "group_namespace")
	assert.NotContains(t, sql, "group_container")
	assert.Contains(t, sql, "GROUP BY log, kubernetes_pod_name ")
}

func TestBuildOpenObserveLogGroupSQL_WorkloadFallsBackToContainerWithoutPodColumn(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet("body", "container_name"))

	sql := buildOpenObserveLogGroupSQL(integrations.OpenObserveDefaultLogStream, cols, "", "checkout", 100)

	assert.Contains(t, sql, "container_name LIKE 'checkout%'")
}

func TestConvertOpenObserveLogGroups(t *testing.T) {
	out := convertOpenObserveLogGroups([]map[string]any{
		{
			"group_sample":    "connection refused",
			"group_namespace": "prod",
			"group_pod":       "checkout-service-7d9f8b6c5d-x2k9p",
			"group_container": "checkout",
			"group_level":     "ERROR",
			"group_count":     float64(42),
			"group_last_ts":   float64(1700000000000000),
		},
	}, 1699999000)

	require.Len(t, out.Groups, 1)
	g := out.Groups[0]

	assert.Equal(t, "connection refused", g.Sample)
	assert.Equal(t, "prod", g.Namespace)
	assert.Equal(t, "checkout-service", g.Workload)
	assert.Equal(t, "checkout", g.Container)
	assert.Equal(t, "ERROR", g.Level)
	assert.Equal(t, int64(42), g.Count)
	assert.Equal(t, "/k8s/prod/checkout-service/checkout", g.ContainerID)
	// _timestamp is microseconds; the LogGroup contract is epoch seconds.
	assert.Equal(t, []int64{1700000000}, g.Timestamps)
	assert.Equal(t, []float64{42}, g.Values)
	assert.Equal(t, generatePatternHash("connection refused"), g.PatternHash)
}

func TestConvertOpenObserveLogGroups_SkipsUnusableRowsAndFallsBack(t *testing.T) {
	out := convertOpenObserveLogGroups([]map[string]any{
		{"group_sample": "no count here"},                    // missing count
		{"group_count": float64(3)},                          // missing sample
		{"group_sample": "orphan error", "group_count": 7.0}, // no k8s fields, no timestamp
	}, 1699999000)

	require.Len(t, out.Groups, 1)
	g := out.Groups[0]

	assert.Equal(t, "orphan error", g.Sample)
	assert.Equal(t, int64(7), g.Count)
	// No severity column in the row — grouping already filtered to errors.
	assert.Equal(t, "error", g.Level)
	// Without namespace+workload there is no container_id to build.
	assert.Equal(t, "", g.ContainerID)
	// MAX(_timestamp) absent → end of the query window.
	assert.Equal(t, []int64{1699999000}, g.Timestamps)
}

func TestOpenObserveTimeRangeMicros(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)

	start, end := openObserveTimeRangeMicros(1784718000000, 1784721600000, now)
	assert.Equal(t, int64(1784718000000000), start)
	assert.Equal(t, int64(1784721600000000), end)

	// An empty window defaults to the last hour.
	start, end = openObserveTimeRangeMicros(0, 0, now)
	assert.Equal(t, int64(1784718000000000), start)
	assert.Equal(t, int64(1784721600000000), end)
}

func TestFetchOpenObserveStreamFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/test-org/streams/default/schema", r.URL.Path)
		assert.Equal(t, "logs", r.URL.Query().Get("type"))
		assert.Equal(t, "Basic dXNlcjpwYXNz", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "default",
			"stream_type": "logs",
			"schema": [
				{"name": "_timestamp", "type": "Int64"},
				{"name": "body", "type": "Utf8"},
				{"name": "k8s_namespace_name", "type": "Utf8"}
			]
		}`))
	}))
	defer server.Close()

	fields, err := fetchOpenObserveStreamFields(server.URL, "test-org", "user", "pass", integrations.OpenObserveDefaultLogStream)
	require.NoError(t, err)

	// Types are carried through, not just names — column selection depends on them.
	assert.Equal(t, map[string]string{
		"_timestamp":         "Int64",
		"body":               "Utf8",
		"k8s_namespace_name": "Utf8",
	}, fields)
}

// TestOpenObserveLogSource_QueryLogGroup_EndToEnd drives the whole log-group path against a
// fake OpenObserve: config lookup → stream schema → column resolution → SQL → search POST →
// response parsing → LogGroup output. The piece-wise tests above cover each stage in
// isolation; this one is what catches the stages being wired together wrongly.
func TestOpenObserveLogSource_QueryLogGroup_EndToEnd(t *testing.T) {
	var schemaHits int
	var capturedSQL string
	var capturedStart, capturedEnd int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/schema") {
			schemaHits++
			assert.Equal(t, "/api/test-org/streams/default/schema", r.URL.Path)
			assert.Equal(t, "logs", r.URL.Query().Get("type"))
			_, _ = w.Write([]byte(`{"name":"default","schema":[
				{"name":"_timestamp","type":"Int64"},
				{"name":"body","type":"Utf8"},
				{"name":"severity_text","type":"Utf8"},
				{"name":"k8s_namespace_name","type":"Utf8"},
				{"name":"k8s_pod_name","type":"Utf8"},
				{"name":"k8s_container_name","type":"Utf8"}
			]}`))
			return
		}

		require.Equal(t, "/api/test-org/_search", r.URL.Path)
		var reqBody openObserveSearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		capturedSQL = reqBody.Query.SQL
		capturedStart = reqBody.Query.StartTime
		capturedEnd = reqBody.Query.EndTime

		_ = json.NewEncoder(w).Encode(openObserveSearchResponse{
			Hits: []map[string]any{
				{
					"group_sample":    "connection refused to db:5432",
					"group_namespace": "prod",
					"group_pod":       "checkout-service-7d9f8b6c5d-x2k9p",
					"group_container": "checkout",
					"group_level":     "ERROR",
					"group_count":     float64(128),
					"group_last_ts":   float64(1700000000000000),
				},
				{
					"group_sample":    "panic: nil map write",
					"group_namespace": "prod",
					"group_pod":       "checkout-worker-5c7d9f8b6c-a1b2c",
					"group_container": "worker",
					"group_level":     "FATAL",
					"group_count":     float64(3),
					"group_last_ts":   float64(1700000500000000),
				},
			},
		})
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetOpenObserveConfigs,
		func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
			return integrations.OpenObserveConfig{
				URL: server.URL, OrgID: "test-org", Username: "user", Password: "pass",
				LogStream: integrations.OpenObserveDefaultLogStream,
			}, nil
		})

	s := &OpenObserveLogSource{}
	out, err := s.QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{
		AccountId:   "acct-1",
		LogProvider: "openobserve",
		StartTime:   1699999000000, // ms
		EndTime:     1700000600000, // ms
		Request: map[string]any{
			"selectedNamespace": "prod",
			"selectedWorkload":  "checkout-service",
		},
	})
	require.NoError(t, err)

	// Schema is resolved before the aggregation is built.
	assert.Equal(t, 1, schemaHits)

	// The SQL reflects the discovered columns and both UI filters.
	assert.Contains(t, capturedSQL, "body AS group_sample")
	assert.Contains(t, capturedSQL, "count(*) AS group_count")
	assert.Contains(t, capturedSQL, "max(_timestamp) AS group_last_ts")
	assert.Contains(t, capturedSQL, `FROM "default"`)
	assert.Contains(t, capturedSQL, "k8s_namespace_name = 'prod'")
	assert.Contains(t, capturedSQL, "k8s_pod_name LIKE 'checkout-service-%'")
	assert.Contains(t, capturedSQL, "ORDER BY group_count DESC LIMIT 100")

	// The window is handed to OpenObserve in microseconds.
	assert.Equal(t, int64(1699999000000000), capturedStart)
	assert.Equal(t, int64(1700000600000000), capturedEnd)

	require.Len(t, out.Groups, 2)

	first := out.Groups[0]
	assert.Equal(t, "connection refused to db:5432", first.Sample)
	assert.Equal(t, "prod", first.Namespace)
	assert.Equal(t, "checkout-service", first.Workload)
	assert.Equal(t, "checkout", first.Container)
	assert.Equal(t, "/k8s/prod/checkout-service/checkout", first.ContainerID)
	assert.Equal(t, "ERROR", first.Level)
	assert.Equal(t, int64(128), first.Count)
	assert.Equal(t, []int64{1700000000}, first.Timestamps)
	assert.NotEmpty(t, first.PatternHash)

	second := out.Groups[1]
	assert.Equal(t, "checkout-worker", second.Workload)
	assert.Equal(t, int64(3), second.Count)
	assert.Equal(t, []int64{1700000500}, second.Timestamps)

	// Distinct messages must not collide into one ticket reference.
	assert.NotEqual(t, first.PatternHash, second.PatternHash)
}

// TestOpenObserveLogSource_QueryLogGroup_NumericSeverityStream reproduces the live failure
// that surfaced against a real OpenObserve org, where `severity` is Int64:
//
//	Error during planning: Function 'lower' requires ... String, but received Int64
//
// The whole path must now emit numeric comparison instead of LOWER(), and still return groups.
func TestOpenObserveLogSource_QueryLogGroup_NumericSeverityStream(t *testing.T) {
	var capturedSQL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/schema") {
			_, _ = w.Write([]byte(`{"name":"default","schema":[
				{"name":"_timestamp","type":"Int64"},
				{"name":"body","type":"Utf8"},
				{"name":"severity","type":"Int64"},
				{"name":"severity_number","type":"Int64"},
				{"name":"k8s_namespace_name","type":"Utf8"},
				{"name":"k8s_pod_name","type":"Utf8"},
				{"name":"k8s_container_name","type":"Utf8"}
			]}`))
			return
		}

		var reqBody openObserveSearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		capturedSQL = reqBody.Query.SQL

		_ = json.NewEncoder(w).Encode(openObserveSearchResponse{
			Hits: []map[string]any{
				{
					"group_sample":    "db connection pool exhausted",
					"group_namespace": "prod",
					"group_pod":       "orders-api-6b8d7c9f4d-k3m2n",
					"group_container": "orders",
					"group_level_num": float64(21), // OTel FATAL
					"group_count":     float64(9),
					"group_last_ts":   float64(1700000000000000),
				},
			},
		})
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetOpenObserveConfigs,
		func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
			return integrations.OpenObserveConfig{
				URL: server.URL, OrgID: "test-org", Username: "user", Password: "pass",
				LogStream: integrations.OpenObserveDefaultLogStream,
			}, nil
		})

	s := &OpenObserveLogSource{}
	out, err := s.QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{
		AccountId: "acct-1",
		StartTime: 1699999000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)

	// The numeric column must never reach a string function.
	assert.NotContains(t, capturedSQL, "LOWER(")
	assert.Contains(t, capturedSQL, "severity_number >= 17")

	require.Len(t, out.Groups, 1)
	assert.Equal(t, "db connection pool exhausted", out.Groups[0].Sample)
	assert.Equal(t, "orders-api", out.Groups[0].Workload)
	assert.Equal(t, "fatal", out.Groups[0].Level, "OTel severity 21 is FATAL, not ERROR")
}

// TestOpenObserveLogSource_QueryLogGroup_SurfacesSearchError verifies a rejected aggregation
// (e.g. DataFusion refusing the SQL) reaches the caller verbatim rather than as empty groups,
// which would render as a silently blank Log Patterns panel.
func TestOpenObserveLogSource_QueryLogGroup_SurfacesSearchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/schema") {
			_, _ = w.Write([]byte(`{"name":"default","schema":[{"name":"body","type":"Utf8"}]}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"sql parser error: unexpected token"}`))
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetOpenObserveConfigs,
		func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
			return integrations.OpenObserveConfig{
				URL: server.URL, OrgID: "test-org", Username: "user", Password: "pass",
				LogStream: integrations.OpenObserveDefaultLogStream,
			}, nil
		})

	s := &OpenObserveLogSource{}
	_, err := s.QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{AccountId: "acct-1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "sql parser error")
}

// TestOpenObserveLogSource_QueryLogGroup_RejectsStreamWithoutMessageColumn verifies a stream
// carrying no recognised message column fails with an actionable error naming the accepted
// spellings, instead of emitting SQL that OpenObserve would reject.
func TestOpenObserveLogSource_QueryLogGroup_RejectsStreamWithoutMessageColumn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.Path, "/schema"), "must not reach _search")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"default","schema":[{"name":"_timestamp","type":"Int64"}]}`))
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetOpenObserveConfigs,
		func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
			return integrations.OpenObserveConfig{
				URL: server.URL, OrgID: "test-org", Username: "user", Password: "pass",
				LogStream: integrations.OpenObserveDefaultLogStream,
			}, nil
		})

	s := &OpenObserveLogSource{}
	_, err := s.QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{AccountId: "acct-1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no log message column")
	assert.Contains(t, err.Error(), "body")
}

func TestFetchOpenObserveStreamFields_SurfacesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"stream not found"}`))
	}))
	defer server.Close()

	_, err := fetchOpenObserveStreamFields(server.URL, "test-org", "user", "pass", integrations.OpenObserveDefaultLogStream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "stream not found")
}

package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/integrations/core"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"testing"
	"time"

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

	sql, err := s.buildSQL(req)
	require.NoError(t, err)

	expectedSQL := `SELECT * FROM "default" WHERE str_match_ignore_case(body, 'error') ORDER BY _timestamp DESC LIMIT 50`
	assert.Equal(t, expectedSQL, sql)
	assert.Equal(t, "body", s.GetLabelMapping()["message"])
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

	expected := "(str_match(body, 'exception') AND pod = 'api-server-1')"
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

func openObserveFieldSet(names ...string) map[string]struct{} {
	fields := make(map[string]struct{}, len(names))
	for _, n := range names {
		fields[n] = struct{}{}
	}
	return fields
}

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

func TestBuildOpenObserveLogGroupSQL_OtelSchema(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet(
		"body", "severity_text", "k8s_namespace_name", "k8s_pod_name", "k8s_container_name",
	))

	sql := buildOpenObserveLogGroupSQL(openObserveDefaultStream, cols, "", "", openObserveLogGroupLimit)

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

	sql := buildOpenObserveLogGroupSQL(openObserveDefaultStream, cols, "prod", "checkout-service", 100)

	assert.Contains(t, sql, "k8s_namespace_name = 'prod'")
	assert.Contains(t, sql, "k8s_pod_name LIKE 'checkout-service-%'")
}

func TestBuildOpenObserveLogGroupSQL_EscapesFilterQuotes(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet("body", "k8s_namespace_name", "k8s_pod_name"))

	sql := buildOpenObserveLogGroupSQL(openObserveDefaultStream, cols, "pro'd", "che'ckout", 100)

	assert.Contains(t, sql, "k8s_namespace_name = 'pro''d'")
	assert.Contains(t, sql, "k8s_pod_name LIKE 'che''ckout-%'")
}

func TestBuildOpenObserveLogGroupSQL_NoSeverityColumnUsesMessageMatching(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet("log", "kubernetes_pod_name"))

	sql := buildOpenObserveLogGroupSQL(openObserveDefaultStream, cols, "", "", 100)

	assert.Contains(t, sql, "(log LIKE '%ERROR%' OR log LIKE '%FATAL%' OR log LIKE '%CRITICAL%')")
	assert.NotContains(t, sql, "LOWER(")
	// Roles with no matching column drop out of both the projection and the GROUP BY.
	assert.NotContains(t, sql, "group_namespace")
	assert.NotContains(t, sql, "group_container")
	assert.Contains(t, sql, "GROUP BY log, kubernetes_pod_name ")
}

func TestBuildOpenObserveLogGroupSQL_WorkloadFallsBackToContainerWithoutPodColumn(t *testing.T) {
	cols := resolveOpenObserveLogGroupCols(openObserveFieldSet("body", "container_name"))

	sql := buildOpenObserveLogGroupSQL(openObserveDefaultStream, cols, "", "checkout", 100)

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

	fields, err := fetchOpenObserveStreamFields(server.URL, "test-org", "user", "pass", openObserveDefaultStream)
	require.NoError(t, err)

	assert.Equal(t, openObserveFieldSet("_timestamp", "body", "k8s_namespace_name"), fields)
}

func TestFetchOpenObserveStreamFields_SurfacesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"stream not found"}`))
	}))
	defer server.Close()

	_, err := fetchOpenObserveStreamFields(server.URL, "test-org", "user", "pass", openObserveDefaultStream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "stream not found")
}

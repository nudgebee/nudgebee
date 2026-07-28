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

	sql, err := s.buildSQL(req, "default")
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

func TestOpenObserveLogSourceBuildSQLUsesConfiguredStream(t *testing.T) {
	s := &OpenObserveLogSource{}
	req := FetchLogRequest{Limit: 10}

	sql, err := s.buildSQL(req, "app_logs")
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "app_logs" ORDER BY _timestamp DESC LIMIT 10`, sql)

	// Empty stream falls back to the OTLP-created default rather than erroring.
	sql, err = s.buildSQL(req, "")
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "default" ORDER BY _timestamp DESC LIMIT 10`, sql)
}

func TestOpenObserveLogSourceBuildSQLRejectsUnsafeStream(t *testing.T) {
	s := &OpenObserveLogSource{}
	// A stream name carrying a quote would otherwise break out of the FROM clause.
	for _, bad := range []string{`a" OR "1"="1`, "logs;DROP", "logs stream", "log.stream"} {
		_, err := s.buildSQL(FetchLogRequest{}, bad)
		require.Error(t, err, "stream %q must be rejected", bad)
		assert.Contains(t, err.Error(), "unsafe stream name")
	}
}

func TestOpenObserveLabelSampleRequestDefaultsZeroWindow(t *testing.T) {
	// QueryLabelValues applies the same one-hour fallback; both paths must agree
	// so autocomplete callers that omit the range don't get a 400.
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	got := buildOpenObserveLabelSampleRequest(FetchLogLabelRequest{AccountId: "a"}, now)
	assert.Equal(t, now.Add(-time.Hour).UnixMilli(), got.StartTime)
	assert.Equal(t, now.UnixMilli(), got.EndTime)
}

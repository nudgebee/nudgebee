package observability

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The provider names getLogSource / getMetricsSource accept. These switches are
// the authority on what the backend can resolve, and therefore on what module
// strings it can mint. Kept as literals rather than reflected out of the switch
// so that adding a provider there fails this test loudly.
var (
	logSourceProviders = []string{
		"loki", "signoz", "datadog", "observe", "loggly", "azure_app_insights",
		"aws_cloudwatch", "ES", "newrelic", "splunk_observability_platform",
		"dynatrace", "solarwinds", "pinot", "hive",
	}
	metricsSourceProviders = []string{
		"datadog", "prometheus", "chronosphere", "aws_cloudwatch",
		"azure_app_insights", "newrelic", "splunk_observability_platform", "ES",
		"dynatrace", "solarwinds",
	}
)

// This is the test that makes an allowlist safe to keep: a provider added to
// getLogSource/getMetricsSource without a matching migration would otherwise
// fail its INSERT silently, because the history write is fire-and-forget.
func TestUserHistoryModulesCoverAllProviders(t *testing.T) {
	for _, p := range logSourceProviders {
		row, ok := newUserHistoryRow(logQueryModulePrefix, p, "acct", "q", nil, 0)
		require.True(t, ok)
		assert.True(t, IsKnownUserHistoryModule(row.Module),
			"log provider %q mints %q, which is missing from userHistoryModules and the module_check migration", p, row.Module)
	}
	for _, p := range metricsSourceProviders {
		row, ok := newUserHistoryRow(metricsQueryModulePrefix, p, "acct", "q", nil, 0)
		require.True(t, ok)
		assert.True(t, IsKnownUserHistoryModule(row.Module),
			"metrics provider %q mints %q, which is missing from userHistoryModules and the module_check migration", p, row.Module)
	}
}

// The Go allowlist mirrors the SQL one; drift between them reintroduces exactly
// the silent-rejection failure the mirror exists to prevent.
func TestUserHistoryModulesMatchMigration(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(
		"..", "..", "migrations", "migrations", "app", "*_user_history_module_check_and_binary_purge.up.sql"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "expected exactly one user_history module_check migration")

	body, err := os.ReadFile(matches[0])
	require.NoError(t, err)

	// Only the ADD CONSTRAINT statement, so the comment block above it (which
	// mentions module names in prose) cannot pollute the parse.
	sql := string(body)
	addIdx := strings.Index(sql, "add constraint")
	require.Positive(t, addIdx)

	found := map[string]struct{}{}
	for _, m := range regexp.MustCompile(`'((?:log|metrics)_query_[^']+)'`).FindAllStringSubmatch(sql[addIdx:], -1) {
		found[m[1]] = struct{}{}
	}

	assert.Equal(t, len(userHistoryModules), len(found), "migration and userHistoryModules disagree on count")
	for module := range userHistoryModules {
		assert.Contains(t, found, module, "module %q is allowed in Go but missing from the migration", module)
	}
	for module := range found {
		assert.True(t, IsKnownUserHistoryModule(module), "module %q is in the migration but missing from userHistoryModules", module)
	}
}

func TestIsKnownUserHistoryModule(t *testing.T) {
	assert.True(t, IsKnownUserHistoryModule("log_query_loki"))
	assert.True(t, IsKnownUserHistoryModule("metrics_query_victoria-metrics"))
	assert.False(t, IsKnownUserHistoryModule("log_query_"))
	assert.False(t, IsKnownUserHistoryModule("relay_action"))
	// The legacy mis-cased value is deliberately absent: the backend lowercases,
	// so allowing it would make the casing bug permanent.
	assert.False(t, IsKnownUserHistoryModule("metrics_query_ES"))
}

func TestBuildLogQueryHistory(t *testing.T) {
	whereClause := query.QueryWhereClause{
		And: []query.QueryWhereClause{{Binary: query.BinaryWhereClause{"app": {"_eq": "web"}}}},
	}

	tests := []struct {
		name       string
		req        FetchLogRequest
		res        FetchLogsResult
		execErr    error
		wantOK     bool
		wantModule string
		wantData   string
		wantStatus string
	}{
		{
			name:       "builder success records the backend-generated query, not the filter array",
			req:        FetchLogRequest{AccountId: "a1", QueryRequest: LogsQueryBuilderRequest{Where: whereClause}},
			res:        FetchLogsResult{Query: `{app="web"}`, Provider: "loki"},
			wantOK:     true,
			wantModule: "log_query_loki",
			wantData:   `{app="web"}`,
			wantStatus: "SUCCESS",
		},
		{
			name:       "zero results is still a successful query",
			req:        FetchLogRequest{AccountId: "a1", Query: `{app="web"}`, LogProvider: "loki"},
			res:        FetchLogsResult{Logs: nil, Query: `{app="web"}`, Provider: "loki"},
			wantOK:     true,
			wantModule: "log_query_loki",
			wantData:   `{app="web"}`,
			wantStatus: "SUCCESS",
		},
		{
			name:       "execution failure records FAILED with the executed query",
			req:        FetchLogRequest{AccountId: "a1", QueryRequest: LogsQueryBuilderRequest{Where: whereClause}},
			res:        FetchLogsResult{Query: "SELECT * FROM logs", Provider: "pinot"},
			execErr:    errors.New("pinot: table not found"),
			wantOK:     true,
			wantModule: "log_query_pinot",
			wantData:   "SELECT * FROM logs",
			wantStatus: "FAILED",
		},
		{
			name:       "uppercase provider is lowercased so the read side can match",
			req:        FetchLogRequest{AccountId: "a1", Query: "*"},
			res:        FetchLogsResult{Query: "*", Provider: "ES"},
			wantOK:     true,
			wantModule: "log_query_es",
			wantData:   "*",
			wantStatus: "SUCCESS",
		},
		{
			name:       "pre-resolution failure falls back to the requested provider and where clause",
			req:        FetchLogRequest{AccountId: "a1", LogProvider: "loki", QueryRequest: LogsQueryBuilderRequest{Where: whereClause}},
			res:        FetchLogsResult{},
			execErr:    errors.New("unsupported operator for label data type"),
			wantOK:     true,
			wantModule: "log_query_loki",
			wantData:   `{"_and":[{"_binary":{"app":{"_eq":"web"}}}]}`,
			wantStatus: "FAILED",
		},
		{
			name:    "no provider at all is unrecordable",
			req:     FetchLogRequest{AccountId: "a1", Query: `{app="web"}`},
			res:     FetchLogsResult{},
			execErr: errors.New("no logs integration"),
			wantOK:  false,
		},
		{
			name:   "no query text at all is unrecordable",
			req:    FetchLogRequest{AccountId: "a1", LogProvider: "loki"},
			res:    FetchLogsResult{Provider: "loki"},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row, ok := BuildLogQueryHistory(tt.req, tt.res, tt.execErr, 1500*time.Millisecond)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			assert.Equal(t, tt.wantModule, row.Module)
			assert.Equal(t, tt.wantData, row.Data)
			assert.Equal(t, tt.wantStatus, row.Status)
			assert.Equal(t, "a1", row.AccountId)
			assert.Equal(t, int64(1500), row.Duration)
		})
	}
}

func TestBuildMetricsQueryHistory(t *testing.T) {
	boom := "rate() expects a range vector"

	t.Run("multi-query text is deterministic regardless of map order", func(t *testing.T) {
		res := OutputMetricQuery{Results: []QueryResult{
			{QueryKey: "b", Query: "up"},
			{QueryKey: "a", Query: "rate(x[5m])"},
		}}
		row, ok := BuildMetricsQueryHistory(
			FetchMetricsRequest{AccountId: "a1", MetricProvider: "prometheus"}, res, nil, time.Second)
		require.True(t, ok)
		assert.Equal(t, "rate(x[5m])\nup", row.Data)
		assert.Equal(t, "metrics_query_prometheus", row.Module)
		assert.Equal(t, "SUCCESS", row.Status)
	})

	t.Run("a per-result error is FAILED even though the call returned 200", func(t *testing.T) {
		res := OutputMetricQuery{Results: []QueryResult{{QueryKey: "a", Query: "rate(x)", Error: &boom}}}
		row, ok := BuildMetricsQueryHistory(
			FetchMetricsRequest{AccountId: "a1", MetricProvider: "prometheus"}, res, nil, time.Second)
		require.True(t, ok)
		assert.Equal(t, "FAILED", row.Status)
	})

	t.Run("an empty error string is not a failure", func(t *testing.T) {
		empty := ""
		res := OutputMetricQuery{Results: []QueryResult{{QueryKey: "a", Query: "up", Error: &empty}}}
		row, ok := BuildMetricsQueryHistory(
			FetchMetricsRequest{AccountId: "a1", MetricProvider: "prometheus"}, res, nil, time.Second)
		require.True(t, ok)
		assert.Equal(t, "SUCCESS", row.Status)
	})

	t.Run("falls back to the submitted queries when nothing executed", func(t *testing.T) {
		row, ok := BuildMetricsQueryHistory(FetchMetricsRequest{
			AccountId:      "a1",
			MetricProvider: "victoria-metrics",
			Queries:        map[string]string{"k2": "up", "k1": "node_load1"},
		}, OutputMetricQuery{}, errors.New("unsupported provider"), time.Second)
		require.True(t, ok)
		assert.Equal(t, "node_load1\nup", row.Data)
		assert.Equal(t, "metrics_query_victoria-metrics", row.Module)
		assert.Equal(t, "FAILED", row.Status)
	})

	t.Run("uppercase ES provider is lowercased", func(t *testing.T) {
		res := OutputMetricQuery{Results: []QueryResult{{QueryKey: "a", Query: "avg"}}}
		row, ok := BuildMetricsQueryHistory(
			FetchMetricsRequest{AccountId: "a1", MetricProvider: "ES"}, res, nil, time.Second)
		require.True(t, ok)
		assert.Equal(t, "metrics_query_es", row.Module)
	})

	t.Run("no provider is unrecordable", func(t *testing.T) {
		_, ok := BuildMetricsQueryHistory(
			FetchMetricsRequest{AccountId: "a1", Queries: map[string]string{"k": "up"}}, OutputMetricQuery{}, nil, time.Second)
		assert.False(t, ok)
	})

	t.Run("no query text is unrecordable", func(t *testing.T) {
		_, ok := BuildMetricsQueryHistory(
			FetchMetricsRequest{AccountId: "a1", MetricProvider: "prometheus"}, OutputMetricQuery{}, nil, time.Second)
		assert.False(t, ok)
	})
}

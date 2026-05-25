package observability

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/services/service"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTracesQuery(t *testing.T) {
	accountId := os.Getenv("TEST_OBSERVABILITY_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_OBSERVABILITY_ACCOUNT_ID not set, skipping traces integration test")
	}
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), accountId, os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		// --- Simple mode (convenience filters) ---
		{
			name: "Simple mode with service name filter",
			params: map[string]any{
				"query_mode":   "simple",
				"service_name": "services-server",
				"duration":     "1h",
				"limit":        10,
			},
		},
		{
			name: "Simple mode with error status filter",
			params: map[string]any{
				"query_mode":   "simple",
				"service_name": "services-server",
				"status":       "error",
				"duration":     "1h",
				"limit":        10,
			},
		},
		{
			name: "Simple mode with min duration (slow traces)",
			params: map[string]any{
				"query_mode":      "simple",
				"service_name":    "services-server",
				"min_duration_ms": 500,
				"duration":        "1h",
				"limit":           10,
			},
		},
		{
			name: "Simple mode with span name filter",
			params: map[string]any{
				"query_mode":   "simple",
				"service_name": "services-server",
				"span_name":    "HTTP GET",
				"duration":     "1h",
				"limit":        10,
			},
		},
		{
			name: "Simple mode with all filters combined",
			params: map[string]any{
				"query_mode":      "simple",
				"service_name":    "services-server",
				"status":          "error",
				"min_duration_ms": 100,
				"span_name":       "HTTP",
				"duration":        "15m",
				"sort_by":         "duration_desc",
				"limit":           5,
			},
		},
		{
			name: "Simple mode defaults (no query_mode specified)",
			params: map[string]any{
				"service_name": "services-server",
				"duration":     "30m",
			},
		},

		// --- Text mode ---
		{
			name: "Text mode with query string",
			params: map[string]any{
				"query_mode": "text",
				"query":      "service.name = \"services-server\"",
				"duration":   "1h",
			},
		},
		{
			name: "Text mode with limit",
			params: map[string]any{
				"query_mode": "text",
				"query":      "service.name = \"services-server\"",
				"limit":      10,
			},
		},
		{
			name: "Text mode with string limit",
			params: map[string]any{
				"query": "service.name = \"services-server\"",
				"limit": "5",
			},
		},

		// --- Structured mode (frontend-style query_request) ---
		{
			name: "Structured mode with empty where",
			params: map[string]any{
				"query_mode": "structured",
				"query_request": map[string]any{
					"where":  map[string]any{"_binary": map[string]any{}},
					"having": map[string]any{},
					"limit":  50,
					"offset": 0,
					"order_by": []map[string]any{
						{"column": "timestamp", "order": "desc"},
					},
				},
				"limit": 50,
			},
		},
		{
			name: "Structured mode with service filter in where clause",
			params: map[string]any{
				"query_mode": "structured",
				"query_request": map[string]any{
					"where": map[string]any{
						"_binary": map[string]any{},
						"_and": []map[string]any{
							{
								"column": "service_name",
								"op":     "_eq",
								"value":  "services-server",
							},
						},
					},
					"limit":  50,
					"offset": 0,
					"order_by": []map[string]any{
						{"column": "timestamp", "order": "desc"},
					},
				},
				"limit": 50,
			},
		},

		// --- Duration / time range options ---
		{
			name: "Duration 15m lookback",
			params: map[string]any{
				"query":    "service.name = \"services-server\"",
				"duration": "15m",
				"limit":    5,
			},
		},
		{
			name: "Duration 6h lookback",
			params: map[string]any{
				"query_mode":   "simple",
				"service_name": "services-server",
				"duration":     "6h",
				"limit":        5,
			},
		},
		{
			name: "Absolute RFC3339 time range",
			params: map[string]any{
				"query":      "service.name = \"services-server\"",
				"start_time": "2026-03-16T14:00:00Z",
				"end_time":   "2026-03-16T15:00:00Z",
			},
		},

		// --- Sort options ---
		{
			name: "Sort by duration descending (slowest first)",
			params: map[string]any{
				"query_mode":   "simple",
				"service_name": "services-server",
				"sort_by":      "duration_desc",
				"duration":     "1h",
				"limit":        10,
			},
		},
		{
			name: "Sort by timestamp ascending (oldest first)",
			params: map[string]any{
				"query_mode":   "simple",
				"service_name": "services-server",
				"sort_by":      "timestamp_asc",
				"duration":     "1h",
				"limit":        10,
			},
		},

		// --- Provider options ---
		{
			name: "With provider source",
			params: map[string]any{
				"query":                 "service.name = \"services-server\"",
				"trace_provider_source": "agent",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
				resultMap, ok := result.(map[string]any)
				assert.True(t, ok, "Result should be a map")
				assert.NotNil(t, resultMap["traces"])
				assert.NotNil(t, resultMap["metadata"])
				traces, ok := resultMap["traces"].([]service.ObservabilityTrace)
				assert.True(t, ok, "traces should be []service.ObservabilityTrace")
				t.Logf("Got %d traces", len(traces))
			}
		})
	}
}

// --- Unit tests (no API needed) ---

func TestTracesMissingAllQueryInputs(t *testing.T) {
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestTracesEmptyQueryNoFilters(t *testing.T) {
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{"query": ""})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestTracesInvalidStartTime(t *testing.T) {
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{
		"query":      "service.name = \"test\"",
		"start_time": "not-a-time",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse start_time")
}

func TestTracesInvalidEndTime(t *testing.T) {
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{
		"query":    "service.name = \"test\"",
		"end_time": "not-a-time",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse end_time")
}

func TestTracesInvalidDuration(t *testing.T) {
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{
		"query":    "service.name = \"test\"",
		"duration": "invalid",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse duration")
}

func TestTracesInvalidLimit(t *testing.T) {
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{
		"query": "service.name = \"test\"",
		"limit": "not-a-number",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse limit")
}

func TestTracesDurationOverridesStartEndTime(t *testing.T) {
	before := time.Now()
	endTime, startTime, err := parseTimeRange(map[string]any{
		"duration":   "15m",
		"start_time": "2020-01-01T00:00:00Z",
		"end_time":   "2020-01-01T01:00:00Z",
	})
	after := time.Now()

	assert.NoError(t, err)
	assert.True(t, endTime.After(before) || endTime.Equal(before))
	assert.True(t, endTime.Before(after) || endTime.Equal(after))
	assert.InDelta(t, 15*time.Minute, endTime.Sub(startTime), float64(2*time.Second))
}

func TestTracesDurationParsesCorrectly(t *testing.T) {
	cases := []struct {
		duration string
		expected time.Duration
	}{
		{"5m", 5 * time.Minute},
		{"1h", 1 * time.Hour},
		{"24h", 24 * time.Hour},
		{"30m", 30 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.duration, func(t *testing.T) {
			endTime, startTime, err := parseTimeRange(map[string]any{"duration": tc.duration})
			assert.NoError(t, err)
			assert.InDelta(t, tc.expected, endTime.Sub(startTime), float64(2*time.Second))
		})
	}
}

func TestTracesBuildSimpleFilters(t *testing.T) {
	qr := buildQueryRequestFromSimpleFilters(map[string]any{
		"service_name":    "my-service",
		"status":          "error",
		"span_name":       "HTTP GET",
		"min_duration_ms": 500,
	})

	assert.Equal(t, "my-service", qr.Where.Binary["ServiceName"][service.Eq])
	assert.Equal(t, "STATUS_CODE_ERROR", qr.Where.Binary["StatusCode"][service.Eq])
	assert.Equal(t, "HTTP GET", qr.Where.Binary["SpanName"][service.Like])
	assert.Equal(t, int64(500_000_000), qr.Where.Binary["Duration"][service.Gte])
}

func TestTracesSortByMapping(t *testing.T) {
	cases := []struct {
		sortBy string
		column string
		order  string
	}{
		{"timestamp_desc", "timestamp", "desc"},
		{"timestamp_asc", "timestamp", "asc"},
		{"duration_desc", "duration", "desc"},
		{"duration_asc", "duration", "asc"},
	}

	for _, tc := range cases {
		t.Run(tc.sortBy, func(t *testing.T) {
			orderBy := sortByToOrderBy(tc.sortBy)
			assert.Len(t, orderBy, 1)
			assert.Equal(t, tc.column, orderBy[0].Column)
			assert.Equal(t, service.QuerySortOrder(tc.order), orderBy[0].Order)
		})
	}

	assert.Nil(t, sortByToOrderBy("unknown"))
}

func TestTracesWithAccountId(t *testing.T) {
	task := &TracesTask{}
	accountId := os.Getenv("TEST_OBSERVABILITY_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_OBSERVABILITY_ACCOUNT_ID not set")
	}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), accountId, os.Getenv("TEST_USER_ID"), slog.Default())

	result, err := task.Execute(taskCtx, map[string]any{
		"query":      "service.name = \"services-server\"",
		"account_id": accountId,
		"limit":      5,
	})
	assert.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	assert.True(t, ok, "Result should be a map")
	assert.NotNil(t, resultMap["traces"])
	assert.NotNil(t, resultMap["metadata"])
}

func TestTracesGetName(t *testing.T) {
	task := &TracesTask{}
	assert.Equal(t, "observability.traces", task.GetName())
}

func TestTracesGetDescription(t *testing.T) {
	task := &TracesTask{}
	assert.Equal(t, "Query Traces.", task.GetDescription())
}

func TestTracesGetDisplayName(t *testing.T) {
	task := &TracesTask{}
	assert.Equal(t, "Query Traces", task.GetDisplayName())
}

func TestTracesInputSchema(t *testing.T) {
	task := &TracesTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)

	// Query mode and filters
	assert.Contains(t, schema.Properties, "query_mode")
	assert.Contains(t, schema.Properties, "service_name")
	assert.Contains(t, schema.Properties, "status")
	assert.Contains(t, schema.Properties, "min_duration_ms")
	assert.Contains(t, schema.Properties, "span_name")
	assert.Contains(t, schema.Properties, "query")
	assert.Contains(t, schema.Properties, "query_request")

	// Time
	assert.Contains(t, schema.Properties, "duration")
	assert.Contains(t, schema.Properties, "start_time")
	assert.Contains(t, schema.Properties, "end_time")

	// Pagination & sort
	assert.Contains(t, schema.Properties, "sort_by")
	assert.Contains(t, schema.Properties, "limit")
	assert.Contains(t, schema.Properties, "offset")

	// Provider
	assert.Contains(t, schema.Properties, "account_id")
	assert.Contains(t, schema.Properties, "trace_provider")
	assert.Contains(t, schema.Properties, "trace_provider_source")

	// Simple mode fields have VisibleWhen
	assert.NotNil(t, schema.Properties["service_name"].VisibleWhen)
	assert.Equal(t, "query_mode", schema.Properties["service_name"].VisibleWhen.Field)

	// Defaults
	assert.Equal(t, "simple", schema.Properties["query_mode"].Default)
	assert.Equal(t, "1h", schema.Properties["duration"].Default)
	assert.Equal(t, "timestamp_desc", schema.Properties["sort_by"].Default)
	assert.Equal(t, 100, schema.Properties["limit"].Default)
}

func TestTracesOutputSchema(t *testing.T) {
	task := &TracesTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "traces")
	assert.Contains(t, schema.Properties, "metadata")
}

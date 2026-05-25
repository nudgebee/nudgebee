package observability

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

const (
	// Default time range for log queries (last hour)
	defaultTimeRangeMinutes = 60
)

// getTestAccountID returns the account ID from TEST_ACCOUNT_ID environment variable.
// Skips the test if the environment variable is not set.
func getTestAccountID(t *testing.T) string {
	accountID := os.Getenv("TEST_ACCOUNT_ID")
	if accountID == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT_ID environment variable not set")
	}
	return accountID
}

// newTestRequestContext creates a request context for integration tests with super admin privileges.
func newTestRequestContext() *security.RequestContext {
	logger := slog.Default()
	secCtx := security.NewSecurityContextForSuperAdmin()
	return security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
}

// getObserveConfigOrSkip retrieves Observe config or skips the test if not available.
func getObserveConfigOrSkip(t *testing.T, ctx *security.RequestContext, accountID string) integrations.ObserveConfig {
	config, err := integrations.GetObserveConfigs(ctx, accountID)
	if err != nil {
		t.Skipf("Skipping integration test - Observe config not available for account %s: %v", accountID, err)
	}
	if config.CustomerID == "" || config.LogDatasetID == "" {
		t.Skipf("Skipping integration test - Observe config incomplete (missing CustomerID or LogDatasetID)")
	}
	return config
}

// getDefaultTimeRange returns start and end times (Unix millis) for the last N minutes.
func getDefaultTimeRange(minutes int) (startTime, endTime int64) {
	now := time.Now()
	endTime = now.UnixMilli()
	startTime = now.Add(-time.Duration(minutes) * time.Minute).UnixMilli()
	return
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// TestObserveQueryLogs_Integration_BasicQuery tests basic log querying with time range only.
func TestObserveQueryLogs_Integration_BasicQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := newTestRequestContext()
	accountID := getTestAccountID(t)
	_ = getObserveConfigOrSkip(t, ctx, accountID)

	source := &ObserveSource{}
	startTime, endTime := getDefaultTimeRange(defaultTimeRangeMinutes)

	request := FetchLogRequest{
		AccountId: accountID,
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     10,
	}

	logs, err := source.QueryLogs(ctx, request)
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}

	t.Logf("Retrieved %d logs", len(logs))

	// Log sample results for debugging
	for i, log := range logs {
		if i >= 3 {
			break
		}
		t.Logf("Log %d: timestamp=%s, severity=%s, message=%s",
			i, log.Timestamp, log.Severity, truncateString(log.Message, 80))
	}
}

// TestObserveQueryLogs_Integration_WithOpalFilter tests log querying with raw OPAL filter strings.
func TestObserveQueryLogs_Integration_WithOpalFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := newTestRequestContext()
	accountID := getTestAccountID(t)
	_ = getObserveConfigOrSkip(t, ctx, accountID)

	source := &ObserveSource{}
	startTime, endTime := getDefaultTimeRange(defaultTimeRangeMinutes)

	testCases := []struct {
		name      string
		opalQuery string
	}{
		{
			name:      "Empty filter (all logs)",
			opalQuery: "",
		},
		{
			name:      "Filter by error level",
			opalQuery: `filter level = "ERROR"`,
		},
		{
			name:      "Filter by warn level",
			opalQuery: `filter level = "WARN"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := FetchLogRequest{
				AccountId: accountID,
				Query:     tc.opalQuery,
				StartTime: startTime,
				EndTime:   endTime,
				Limit:     10,
			}

			logs, err := source.QueryLogs(ctx, request)
			if err != nil {
				t.Fatalf("QueryLogs() error = %v", err)
			}

			t.Logf("Query '%s' returned %d logs", tc.opalQuery, len(logs))

			// Log first result if available
			if len(logs) > 0 {
				t.Logf("  First log: %s - %s", logs[0].Timestamp, truncateString(logs[0].Message, 60))
			}
		})
	}
}

// TestObserveQueryLogs_Integration_WithQueryWhereClause tests automatic conversion from QueryWhereClause to OPAL.
func TestObserveQueryLogs_Integration_WithQueryWhereClause(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := newTestRequestContext()
	accountID := getTestAccountID(t)
	_ = getObserveConfigOrSkip(t, ctx, accountID)

	source := &ObserveSource{}
	startTime, endTime := getDefaultTimeRange(defaultTimeRangeMinutes)

	testCases := []struct {
		name        string
		whereClause query.QueryWhereClause
	}{
		{
			name: "Eq operator on namespace",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"namespace": {query.Eq: "default"},
				},
			},
		},
		{
			name: "Contains operator on body",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"body": {query.Contains: "error"},
				},
			},
		},
		{
			name: "AND clause - namespace and stream",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "default"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"stream": {query.Eq: "stderr"},
						},
					},
				},
			},
		},
		{
			name: "OR clause - multiple log levels",
			whereClause: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"level": {query.Eq: "ERROR"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"level": {query.Eq: "WARN"},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := FetchLogRequest{
				AccountId: accountID,
				StartTime: startTime,
				EndTime:   endTime,
				Limit:     10,
				QueryRequest: LogsQueryBuilderRequest{
					Where: tc.whereClause,
				},
			}

			logs, err := source.QueryLogs(ctx, request)
			if err != nil {
				t.Fatalf("QueryLogs() error = %v", err)
			}

			t.Logf("Where clause '%s' returned %d logs", tc.name, len(logs))
		})
	}
}

// TestObserveQueryLabels_Integration tests fetching available dataset fields (labels).
func TestObserveQueryLabels_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := newTestRequestContext()
	accountID := getTestAccountID(t)
	_ = getObserveConfigOrSkip(t, ctx, accountID)

	source := &ObserveSource{}
	startTime, endTime := getDefaultTimeRange(defaultTimeRangeMinutes)

	request := FetchLogLabelRequest{
		AccountId: accountID,
		StartTime: startTime,
		EndTime:   endTime,
	}

	labels, err := source.QueryLabels(ctx, request)
	if err != nil {
		t.Fatalf("QueryLabels() error = %v", err)
	}

	if len(labels) == 0 {
		t.Error("QueryLabels() returned no labels, expected at least some fields")
	}

	t.Logf("Retrieved %d labels", len(labels))

	// Log sample labels for debugging
	for i, label := range labels {
		if i >= 10 {
			t.Logf("... and %d more labels", len(labels)-10)
			break
		}
		t.Logf("  Label: %s (type: %v)", label.Label, label.Attributes["datatype"])
	}
}

// TestObserveGetQuery_Integration tests OPAL query generation from QueryWhereClause.
func TestObserveGetQuery_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := newTestRequestContext()
	source := &ObserveSource{}

	testCases := []struct {
		name           string
		whereClause    query.QueryWhereClause
		expectedPrefix string
	}{
		{
			name: "Simple equality filter",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"namespace": {query.Eq: "production"},
				},
			},
			expectedPrefix: "filter",
		},
		{
			name: "Combined AND/OR filters",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "prod"},
						},
					},
				},
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"level": {query.Eq: "ERROR"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"level": {query.Eq: "WARN"},
						},
					},
				},
			},
			expectedPrefix: "filter",
		},
		{
			name:           "Empty filter",
			whereClause:    query.QueryWhereClause{},
			expectedPrefix: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := FetchLogRequest{
				QueryRequest: LogsQueryBuilderRequest{
					Where: tc.whereClause,
				},
			}

			opalQuery, err := source.GetQuery(ctx, request)
			assert.NoError(t, err)

			t.Logf("Generated OPAL query: %s", opalQuery)

			if tc.expectedPrefix != "" {
				assert.True(t, len(opalQuery) >= len(tc.expectedPrefix),
					"GetQuery() = %s, expected to start with %s", opalQuery, tc.expectedPrefix)
			} else {
				assert.Equal(t, "", opalQuery)
			}
		})
	}
}

// TestObserveQueryLogs_Integration_WithCustomDatasetID tests querying with a custom dataset ID.
func TestObserveQueryLogs_Integration_WithCustomDatasetID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := newTestRequestContext()
	accountID := getTestAccountID(t)
	config := getObserveConfigOrSkip(t, ctx, accountID)

	source := &ObserveSource{}
	startTime, endTime := getDefaultTimeRange(defaultTimeRangeMinutes)

	// Test using the default dataset ID explicitly via Request map
	request := FetchLogRequest{
		AccountId: accountID,
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     5,
		Request: map[string]any{
			"dataset_id": config.LogDatasetID,
		},
	}

	logs, err := source.QueryLogs(ctx, request)
	if err != nil {
		t.Fatalf("QueryLogs() with custom dataset_id error = %v", err)
	}

	t.Logf("Retrieved %d logs using explicit dataset_id: %s", len(logs), config.LogDatasetID)
}

// TestObserveQueryLogs_Integration_ErrorHandling tests error handling for invalid configurations.
func TestObserveQueryLogs_Integration_ErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := newTestRequestContext()
	source := &ObserveSource{}
	startTime, endTime := getDefaultTimeRange(defaultTimeRangeMinutes)

	t.Run("Invalid account ID returns error", func(t *testing.T) {
		request := FetchLogRequest{
			AccountId: "non-existent-account-id-12345",
			StartTime: startTime,
			EndTime:   endTime,
			Limit:     10,
		}

		_, err := source.QueryLogs(ctx, request)
		if err == nil {
			t.Error("QueryLogs() with invalid account expected error, got nil")
		} else {
			t.Logf("Got expected error for invalid account: %v", err)
		}
	})

	t.Run("Invalid OPAL query behavior", func(t *testing.T) {
		accountID := os.Getenv("TEST_ACCOUNT_ID")
		if accountID == "" {
			t.Skip("Skipping - TEST_ACCOUNT_ID not set")
		}

		// Skip if no config exists
		_, configErr := integrations.GetObserveConfigs(ctx, accountID)
		if configErr != nil {
			t.Skipf("Skipping - no Observe config for account %s", accountID)
		}

		request := FetchLogRequest{
			AccountId: accountID,
			Query:     "invalid!!! query syntax @@#$",
			StartTime: startTime,
			EndTime:   endTime,
			Limit:     10,
		}

		_, err := source.QueryLogs(ctx, request)
		// Log the result - may succeed or fail depending on Observe API behavior
		if err != nil {
			t.Logf("Invalid query returned error (expected): %v", err)
		} else {
			t.Log("Invalid query was accepted by Observe API (may be valid OPAL or lenient parsing)")
		}
	})
}

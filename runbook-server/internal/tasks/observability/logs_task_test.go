package observability

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/services/service"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogs_Query(t *testing.T) {
	if os.Getenv("TEST_OBSERVABILITY_ACCOUNT_ID") == "" {
		t.Skip("TEST_OBSERVABILITY_ACCOUNT_ID not set, skipping observability logs integration test")
	}
	task := &LogsTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_OBSERVABILITY_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expected      any
		expectErr     bool
		expectedError string
	}{
		{
			name: "Simple Command Execution",
			params: map[string]any{
				"query": "{namespace=\"nudgebee\"}",
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
				if !assert.NoError(t, err) || !assert.NotNil(t, result) {
					return
				}
				resultMap, ok := result.(map[string]any)
				assert.True(t, ok, "Result should be a map")
				assert.NotNil(t, resultMap["logs"])
				assert.NotNil(t, resultMap["metadata"])
			}
		})
	}
}

func TestLogs_Query_AWS_CloudWatch(t *testing.T) {
	task := &LogsTask{}
	accountId := os.Getenv("TEST_AWS_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_AWS_ACCOUNT_ID not set, skipping AWS CloudWatch logs test")
	}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), accountId, os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name: "AWS CloudWatch logs query",
			params: map[string]any{
				"account_id":            accountId,
				"account_provider_type": accountProviderAWS,
				"query":                 "fields @timestamp, @message | sort @timestamp desc | limit 10",
				"region":                os.Getenv("TEST_AWS_REGION"),
				"log_group":             os.Getenv("TEST_AWS_LOG_GROUP"),
				"limit":                 10,
			},
		},
		{
			name: "AWS CloudWatch logs query with different limit",
			params: map[string]any{
				"account_id":            accountId,
				"account_provider_type": accountProviderAWS,
				"query":                 "fields @timestamp, @message | sort @timestamp desc | limit 5",
				"region":                os.Getenv("TEST_AWS_REGION"),
				"log_group":             os.Getenv("TEST_AWS_LOG_GROUP"),
				"limit":                 5,
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
				assert.NotNil(t, resultMap["logs"])
				assert.NotNil(t, resultMap["metadata"])
				logs, ok := resultMap["logs"].([]service.ObservabilityLog)
				assert.True(t, ok, "logs should be []service.ObservabilityLog")
				t.Logf("AWS CloudWatch: got %d logs", len(logs))
				if len(logs) > 0 {
					t.Logf("AWS CloudWatch: first log = %+v", logs[0])
				}
			}
		})
	}
}

func TestLogs_Query_Azure(t *testing.T) {
	task := &LogsTask{}
	accountId := os.Getenv("TEST_AZURE_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_AZURE_ACCOUNT_ID not set, skipping Azure logs test")
	}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), accountId, os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name: "Azure Log Analytics AzureMetrics query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "AzureMetrics | order by TimeGenerated desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure Log Analytics Usage query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "Usage | order by TimeGenerated desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   5,
			},
		},
		{
			name: "Azure Log Analytics with exact failing workflow payload",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "AzureDiagnostics | project TimeGenerated, Message, Category | order by TimeGenerated desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
			},
		},
		{
			name: "Azure Heartbeat query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "Heartbeat | summarize count() by Computer | order by count_ desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure Perf counter query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "Perf | where ObjectName == 'Processor' | summarize avg(CounterValue) by Computer, bin(TimeGenerated, 1h) | order by TimeGenerated desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure Event log query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "Event | where EventLevelName == 'Error' | project TimeGenerated, Source, EventLog, RenderedDescription | order by TimeGenerated desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure Syslog query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "Syslog | where SeverityLevel == 'err' | project TimeGenerated, Computer, Facility, SyslogMessage | order by TimeGenerated desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name:      "Azure SecurityEvent query (table may not exist)",
			expectErr: true,
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "SecurityEvent | summarize count() by Activity | order by count_ desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure AzureActivity query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "AzureActivity | where CategoryValue == 'Administrative' | project TimeGenerated, Caller, OperationNameValue, ActivityStatusValue | order by TimeGenerated desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure Usage table size query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "Usage | where IsBillable == true | summarize TotalGB = sum(Quantity) / 1024 by DataType | order by TotalGB desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure union search query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "search '*' | summarize count() by $table | order by count_ desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure AzureDiagnostics summarize by category",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "AzureDiagnostics | summarize count() by Category, ResourceType | order by count_ desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
			},
		},
		{
			name: "Azure AzureMetrics time chart query",
			params: map[string]any{
				"account_id":              accountId,
				"account_provider_type":   accountProviderAzure,
				"query":                   "AzureMetrics | where MetricName == 'cpu_percent' | summarize avg(Average) by bin(TimeGenerated, 1h), Resource | order by TimeGenerated desc",
				"log_analytics_workspace": os.Getenv("TEST_AZURE_LOG_ANALYTICS_WORKSPACE"),
				"limit":                   10,
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
				assert.NotNil(t, resultMap["logs"])
				assert.NotNil(t, resultMap["metadata"])
				logs, ok := resultMap["logs"].([]service.ObservabilityLog)
				assert.True(t, ok, "logs should be []service.ObservabilityLog")
				t.Logf("Azure: got %d logs", len(logs))
				if len(logs) > 0 {
					t.Logf("Azure: first log = %+v", logs[0])
				}
			}
		})
	}
}

func TestLogs_Query_K8s_ES(t *testing.T) {
	task := &LogsTask{}
	accountId := os.Getenv("TEST_K8S_ES_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_K8S_ES_ACCOUNT_ID not set, skipping k8s ES logs test")
	}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), accountId, os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name: "K8s ES DSL query with index",
			params: map[string]any{
				"account_id":            accountId,
				"account_provider_type": accountProviderK8sES,
				"query":                 `{"query":{"match_all":{}}}`,
				"index":                 os.Getenv("TEST_ES_INDEX"),
				"query_type":            "dsl",
				"limit":                 10,
			},
		},
		{
			name: "K8s ES query without index (uses account default)",
			params: map[string]any{
				"account_id":            accountId,
				"account_provider_type": accountProviderK8sES,
				"query":                 `{"query":{"match_all":{}}}`,
				"query_type":            "dsl",
				"limit":                 5,
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
				assert.NotNil(t, resultMap["logs"])
				assert.NotNil(t, resultMap["metadata"])
				logs, ok := resultMap["logs"].([]service.ObservabilityLog)
				assert.True(t, ok, "logs should be []service.ObservabilityLog")
				t.Logf("K8s ES: got %d logs", len(logs))
				if len(logs) > 0 {
					t.Logf("K8s ES: first log = %+v", logs[0])
				}
			}
		})
	}
}

func TestLogs_Query_MissingQuery(t *testing.T) {
	task := &LogsTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "query is requried")
}

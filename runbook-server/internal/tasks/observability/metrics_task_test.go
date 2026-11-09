package observability

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/services/service"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_Query_AWS_CloudWatch(t *testing.T) {
	task := &MetricsTask{}
	accountId := os.Getenv("TEST_AWS_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_AWS_ACCOUNT_ID not set, skipping AWS CloudWatch metrics test")
	}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), accountId, os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name: "AWS CloudWatch metrics via account_provider_type",
			params: map[string]any{
				"account_id":            accountId,
				"account_provider_type": "aws",
				"queries":               map[string]string{"A": ""},
				"service_name":          "AWS/EC2",
				"region":                os.Getenv("TEST_AWS_REGION"),
				"resource_ids":          []any{os.Getenv("TEST_AWS_RESOURCE_ID")},
				"resource_type":         "instance",
				"metric_names":          []any{"CPUUtilization"},
				"statistics":            []any{"Average"},
			},
		},
		{
			name: "AWS CloudWatch metrics with different statistic",
			params: map[string]any{
				"account_id":            accountId,
				"account_provider_type": "aws",
				"queries":               map[string]string{"A": ""},
				"service_name":          "AWS/EC2",
				"region":                os.Getenv("TEST_AWS_REGION"),
				"resource_ids":          []any{os.Getenv("TEST_AWS_RESOURCE_ID")},
				"resource_type":         "instance",
				"metric_names":          []any{"CPUUtilization"},
				"statistics":            []any{"Maximum"},
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
				assert.NotNil(t, resultMap["metrics"])
				t.Logf("AWS CloudWatch metrics: got %v results", resultMap["metrics"])
			}
		})
	}
}

func TestMetrics_Query_Prometheus(t *testing.T) {
	task := &MetricsTask{}
	accountId := os.Getenv("TEST_OBSERVABILITY_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_OBSERVABILITY_ACCOUNT_ID not set, skipping Prometheus metrics test")
	}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), accountId, os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name      string
		params    map[string]any
		expectErr bool
	}{
		{
			name: "K8s account - prometheus resolved by services-server",
			params: map[string]any{
				"account_provider_type": "k8s",
				"query":                 "up",
			},
		},
		{
			name: "K8s account - multiple queries",
			params: map[string]any{
				"account_provider_type": "k8s",
				"queries": map[string]string{
					"cpu":    "rate(container_cpu_usage_seconds_total{namespace=\"nudgebee\"}[5m])",
					"memory": "container_memory_usage_bytes{namespace=\"nudgebee\"}",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				resultMap, ok := result.(map[string]any)
				assert.True(t, ok, "Result should be a map")
				assert.NotNil(t, resultMap["metrics"])
				t.Logf("Prometheus metrics: got %v results", resultMap["metrics"])
			}
		})
	}
}

func TestMetrics_Query_MissingQueries(t *testing.T) {
	task := &MetricsTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported queries format or missing")
}

func TestMetrics_Query_GCP_Unsupported(t *testing.T) {
	task := &MetricsTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{
		"account_provider_type": "gcp",
		"queries":               map[string]string{"A": "test"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GCP cloud accounts are not yet supported")
}

// metricsServer stands in for services-server's /rpc/metrics and answers every
// request with body, the way the real one answers HTTP 200 even when a query failed.
func metricsServer(t *testing.T, body string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	previous := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = server.URL
	t.Cleanup(func() { config.Config.ServiceEndpoint = previous })
}

func TestMetrics_Query_InvalidQueryFailsTheTask(t *testing.T) {
	metricsServer(t, `{"results":[{"query_key":"q1","payload":[],"query":"this is not promql(((","error":"1:6: parse error: unexpected identifier \"is\""}]}`)

	task := &MetricsTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())
	result, err := task.Execute(taskCtx, map[string]any{
		"metric_provider": "prometheus",
		"queries":         map[string]any{"q1": "this is not promql((("},
	})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "q1")
	assert.Contains(t, err.Error(), "parse error: unexpected identifier")
}

func TestMetrics_Query_OneFailedQueryAmongSeveralFailsTheTask(t *testing.T) {
	metricsServer(t, `{"results":[`+
		`{"query_key":"good","payload":[{"metric":{"job":"api"},"timestamps":[1],"values":[1]}]},`+
		`{"query_key":"bad","payload":[],"error":"parse error"},`+
		`{"query_key":"also_bad","payload":[],"error":"query timed out"}]}`)

	task := &MetricsTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())
	_, err := task.Execute(taskCtx, map[string]any{
		"queries": map[string]any{"good": "up", "bad": "up((", "also_bad": "up"},
	})

	require.Error(t, err)
	// Every failed key is named, in a stable order; the key that worked is not.
	assert.Contains(t, err.Error(), "also_bad: query timed out; bad: parse error")
	assert.NotContains(t, err.Error(), "good")
}

func TestMetrics_Query_NoDataStillCompletes(t *testing.T) {
	metricsServer(t, `{"results":[{"query_key":"q1","payload":[],"query":"up{job=\"nothing\"}"}]}`)

	task := &MetricsTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())
	result, err := task.Execute(taskCtx, map[string]any{
		"queries": map[string]any{"q1": `up{job="nothing"}`},
	})

	require.NoError(t, err)
	out, ok := result.(map[string]any)
	require.True(t, ok)
	results, ok := out["metrics"].([]service.ObservabilityMetricQueryResult)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.Equal(t, "q1", results[0].QueryKey)
	assert.Empty(t, results[0].ObservabilityMetricQueryPayload)
	assert.Nil(t, results[0].Error)
}

func TestMetrics_Query_EmptyErrorStringIsNotAFailure(t *testing.T) {
	// services-server treats an empty error string as "no error" (noteFailedKeys,
	// query history); the task reads it the same way.
	metricsServer(t, `{"results":[{"query_key":"q1","payload":[],"error":""}]}`)

	task := &MetricsTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())
	_, err := task.Execute(taskCtx, map[string]any{
		"queries": map[string]any{"q1": "up"},
	})

	require.NoError(t, err)
}

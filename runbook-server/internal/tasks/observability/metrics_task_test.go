package observability

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMetrics_Query(t *testing.T) {
	task := &MetricsTask{}
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
				"query": "container_application_type",
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
				assert.NotNil(t, result.(map[string]any)["metrics"])
			}
		})
	}
}

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

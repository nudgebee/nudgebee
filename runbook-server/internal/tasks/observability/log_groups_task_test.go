package observability

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/services/service"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogGroups_List_WithMetricProvider(t *testing.T) {
	task := &LogGroupsTask{}
	accountId := os.Getenv("TEST_OBSERVABILITY_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_OBSERVABILITY_ACCOUNT_ID not set, skipping")
	}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), accountId, os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name      string
		params    map[string]any
		expectErr bool
	}{
		{
			name: "List Log Groups with explicit prometheus provider",
			params: map[string]any{
				"namespace":              "nudgebee",
				"metric_provider":        "prometheus",
				"metric_provider_source": "agent",
			},
		},
		{
			name: "List Log Groups with namespace and workload",
			params: map[string]any{
				"namespace":              "nudgebee",
				"workload":               "services-server",
				"metric_provider":        "prometheus",
				"metric_provider_source": "agent",
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
				assert.NotNil(t, result)
				resultMap, ok := result.(map[string]any)
				assert.True(t, ok, "Result should be a map")
				assert.NotNil(t, resultMap["groups"], "Result should contain 'groups' key")

				groups, ok := resultMap["groups"].([]service.ObservabilityLog)
				assert.True(t, ok, "Groups should be a slice of ObservabilityLog")
				t.Logf("Found %d log groups", len(groups))
			}
		})
	}
}

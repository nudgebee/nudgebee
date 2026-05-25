package events

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStoreTask_Execute(t *testing.T) {
	task := &EventsStoreTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name: "Simple Command Execution",
			params: map[string]any{
				"event": map[string]any{
					"title":           "Sample Event from Workflow",
					"description":     "Very complicated Event",
					"aggregation_key": "workflow_integration_test",
					"finding_id":      "workflow_integration_test::1",
					"finding_type":    "workflow_integration_test",
					"subject_name":    "TestStoreTask_Execute",
					"source":          "workflow",
					"status":          "RESOLVED",
				},
			},
		},
		{
			name: "Cluster derived from account name",
			params: map[string]any{
				"event": map[string]any{
					"title":           "Cluster Fallback Event",
					"description":     "Verifies cluster is filled in from the account",
					"aggregation_key": "workflow_integration_test_cluster_fallback",
					"finding_id":      "workflow_integration_test_cluster_fallback::1",
					"finding_type":    "workflow_integration_test",
					"subject_name":    "TestStoreTask_Execute_ClusterFallback",
					"source":          "workflow",
					"status":          "RESOLVED",
				},
			},
		},
		{
			name: "Invalid status surfaces error",
			params: map[string]any{
				"event": map[string]any{
					"title":           "Bad Status Event",
					"description":     "UI sent a status not in the enum",
					"aggregation_key": "workflow_integration_test_bad_status",
					"finding_id":      "workflow_integration_test_bad_status::1",
					"finding_type":    "workflow_integration_test",
					"subject_name":    "TestStoreTask_Execute_BadStatus",
					"source":          "workflow",
					"status":          "active",
				},
			},
			expectErr:     true,
			expectedError: "events_status",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				if assert.Error(t, err) && tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
				return
			}

			assert.NoError(t, err)
			id, ok := result.(string)
			assert.True(t, ok, "expected event id string, got %T", result)
			assert.NotEmpty(t, id, "expected non-empty event id")
		})
	}
}

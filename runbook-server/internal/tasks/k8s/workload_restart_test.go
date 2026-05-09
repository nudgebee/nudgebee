package k8s

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkloadRestartTask_InputSchema(t *testing.T) {
	task := &WorkloadRestartTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "name")
	assert.Contains(t, schema.Properties, "kind")
}

func TestWorkloadRestartTask_OutputSchema(t *testing.T) {
	task := &WorkloadRestartTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "status")
	assert.Contains(t, schema.Properties, "message")
	assert.Contains(t, schema.Properties, "restarted_kind")
	assert.Contains(t, schema.Properties, "restarted_name")
}

func TestWorkloadRestartTask_Execute_Validation(t *testing.T) {
	task := &WorkloadRestartTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name:          "Missing Namespace",
			params:        map[string]any{"name": "test", "kind": "Deployment"},
			expectErr:     true,
			expectedError: "namespace, name, and kind are required",
		},
		{
			name:          "Unsupported Kind",
			params:        map[string]any{"namespace": "default", "name": "test", "kind": "Service"},
			expectErr:     true,
			expectedError: "workload type 'Service' is not supported",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := task.Execute(taskCtx, tc.params)
			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

package k8s

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestK8sCliTask_Execute(t *testing.T) {
	task := &K8sCliTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name           string
		params         map[string]any
		expectedData   string
		expectedError  string
		expectedStderr string
		expectErr      bool
	}{
		{
			name: "Simple Command Execution - kubectl get po",
			params: map[string]any{
				"command": "kubectl get po",
			},
			expectedData: "NAME",
			expectErr:    false,
		},
		{
			name: "Command without kubectl prefix",
			params: map[string]any{
				"command": "get po",
			},
			expectedData: "NAME",
			expectErr:    false,
		},
		{
			name: "Command with actual error",
			params: map[string]any{
				"command": "kubectl invalid-command",
			},
			expectedError: "unknown command", // Expecting an error from the task now
			expectErr:     true,
		},
		{
			name:          "Missing Command Parameter",
			params:        map[string]any{},
			expectErr:     true,
			expectedError: "command is requried",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				assert.Nil(t, result) // Result should be nil on error
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result) // Result should not be nil on success
				resultMap, ok := result.(map[string]any)
				assert.True(t, ok, "Expected result to be of type map[string]any")

				data, dataOk := resultMap["data"].(string)
				assert.True(t, dataOk, "Expected 'data' field in result")
				assert.Contains(t, data, tc.expectedData)

				if tc.expectedStderr != "" {
					stderr, stderrOk := resultMap["stderr"].(string)
					assert.True(t, stderrOk, "Expected 'stderr' field in result")
					assert.Contains(t, stderr, tc.expectedStderr)
				} else {
					assert.NotContains(t, resultMap, "stderr", "Expected no 'stderr' field for successful commands without stderr")
				}
			}
		})
	}
}

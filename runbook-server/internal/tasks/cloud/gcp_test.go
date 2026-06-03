package cloud

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGCPCliTask_Execute(t *testing.T) {
	task := &GCPCliTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_GCP_ACCOUNT_ID"), os.Getenv("TEST_TEST_ID"), slog.Default())

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
				"command": `gcloud auth list --filter=status:ACTIVE --format="value(account)"`,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clean up any potential temporary files from previous runs
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

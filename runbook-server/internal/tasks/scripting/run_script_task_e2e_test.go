//go:build e2e

package scripting

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRunScriptTask_Execute_AWS covers the rows that execute against a live
// AWS SSM target (needs DB + AWS creds). It requires the env vars to be set.
func TestRunScriptTask_Execute_AWS(t *testing.T) {
	testutils.RequireEnv(t, "TEST_TENANT_ID", "TEST_ACCOUNT_ID", "TEST_USER_ID")

	task := &RunScriptTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expected      any
		expectErr     bool
		expectedError string
	}{
		{
			name: "AWS SSM with bash language on Windows instance",
			params: map[string]any{
				"script":        "echo test",
				"language":      "bash",
				"executor_type": "aws_ssm",
				"target_id":     "i-0123456789abcdef0",
				"region":        "us-east-1",
				"account_id":    "test-account-id",
			},
			expectErr:     true,
			expectedError: "UnsupportedPlatformType",
		},
		{
			name: "AWS SSM with powershell language on Windows instance",
			params: map[string]any{
				"script":        "Write-Output 'test'",
				"language":      "powershell",
				"executor_type": "aws_ssm",
				"target_id":     "i-0123456789abcdef0",
				"region":        "us-east-1",
				"account_id":    "test-account-id",
			},
			expected: "test\r\n",
		},
		{
			name: "AWS SSM with python language on Windows instance",
			params: map[string]any{
				"script":        "print('test')",
				"language":      "python",
				"executor_type": "aws_ssm",
				"target_id":     "i-0123456789abcdef0",
				"region":        "us-east-1",
				"account_id":    "test-account-id",
			},
			expectErr:     true,
			expectedError: "UnsupportedPlatformType",
		},
		{
			name: "AWS SSM with javascript language on Windows instance",
			params: map[string]any{
				"script":        "console.log('test')",
				"language":      "javascript",
				"executor_type": "aws_ssm",
				"target_id":     "i-0123456789abcdef0",
				"region":        "us-east-1",
				"account_id":    "test-account-id",
			},
			expectErr:     true,
			expectedError: "UnsupportedPlatformType",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clean up any potential temporary files from previous runs
			if err := os.Remove("/tmp/retry_attempt_count"); err != nil && !os.IsNotExist(err) {
				t.Fatalf("failed to remove retry count file: %v", err)
			}

			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" && err != nil {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				if assert.NoError(t, err) && result != nil {
					assert.Equal(t, tc.expected, result.(map[string]any)["data"])
				}
			}
		})
	}
}

package scripting

import (
	"log/slog"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRunScriptTask_Execute(t *testing.T) {
	// Force local execution for tests
	oldMode := config.Config.TaskScriptExecutionModel
	config.Config.TaskScriptExecutionModel = "local"
	defer func() { config.Config.TaskScriptExecutionModel = oldMode }()

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
			name: "Simple Script Success",
			params: map[string]any{
				"script":        "echo 'Hello World'",
				"executor_type": "local",
			},
			expected: "Hello World",
		},
		{
			name: "Script with Non-Zero Exit Code",
			params: map[string]any{
				"script":        "exit 1",
				"executor_type": "local",
			},
			expectErr:     true,
			expectedError: "script execution failed: exit status 1",
		},
		{
			name: "Script Using Environment Variable",
			params: map[string]any{
				"script":        "echo $MY_TEST_VAR",
				"executor_type": "local",
				"env": map[string]string{
					"MY_TEST_VAR": "EnvValue",
				},
			},
			expected: "EnvValue",
		},
		{
			name: "Script Syntax Error",
			params: map[string]any{
				"script":        "ech 'Syntax Error'", // Typo: ech instead of echo
				"executor_type": "local",
			},
			expectErr:     true,
			expectedError: "script execution failed: exit status 127",
		},
		{
			name: "Missing Script",
			params: map[string]any{
				"executor_type": "local",
				"env": map[string]string{
					"VAR": "VALUE",
				},
			},
			expectErr:     true,
			expectedError: "missing required parameter: 'script'",
		},
		{
			name: "AWS SSM Missing target_id",
			params: map[string]any{
				"script":        "echo test",
				"executor_type": "aws_ssm",
				"region":        "us-east-1",
			},
			expectErr:     true,
			expectedError: "target_id is required for aws_ssm executor",
		},
		{
			name: "AWS SSM Missing region",
			params: map[string]any{
				"script":        "echo test",
				"executor_type": "aws_ssm",
				"target_id":     "i-12345",
			},
			expectErr:     true,
			expectedError: "region is required for aws_ssm executor",
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

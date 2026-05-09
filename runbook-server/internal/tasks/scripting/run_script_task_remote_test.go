package scripting

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRunScriptTask_RemoteValidation(t *testing.T) {
	task := &RunScriptTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectedError string
	}{
		{
			name: "AWS SSM Missing target_id",
			params: map[string]any{
				"script":        "echo test",
				"executor_type": "aws_ssm",
				"region":        "us-east-1",
			},
			expectedError: "target_id is required for aws_ssm executor",
		},
		{
			name: "AWS SSM Missing region",
			params: map[string]any{
				"script":        "echo test",
				"executor_type": "aws_ssm",
				"target_id":     "i-12345",
			},
			expectedError: "region is required for aws_ssm executor",
		},
		{
			name: "SSH Missing integration_id",
			params: map[string]any{
				"script":        "echo test",
				"executor_type": "ssh",
			},
			expectedError: "integration_id is required for ssh executor",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := task.Execute(taskCtx, tc.params)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedError)
		})
	}
}

package scripting

import (
	"errors"
	"log/slog"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// stubExecutor returns a canned result and records the config it was given.
type stubExecutor struct {
	output string
	err    error
	got    executors.ExecutionConfig
}

func (s *stubExecutor) Execute(_ types.TaskContext, config executors.ExecutionConfig) (string, error) {
	s.got = config
	return s.output, s.err
}

func useStubExecutor(t *testing.T, stub *stubExecutor) {
	t.Helper()
	original := newExecutor
	newExecutor = func(executors.ExecutionConfig) (executors.ScriptExecutor, error) { return stub, nil }
	t.Cleanup(func() { newExecutor = original })
}

func TestRunScriptTask_Execute(t *testing.T) {
	task := &RunScriptTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		stub          stubExecutor
		expected      any
		expectedEnv   map[string]string
		expectErr     bool
		expectedError string
	}{
		{
			name:     "Simple Script Success",
			params:   map[string]any{"script": "echo 'Hello World'", "executor_type": "agent"},
			stub:     stubExecutor{output: "Hello World"},
			expected: "Hello World",
		},
		{
			name:          "Script with Non-Zero Exit Code",
			params:        map[string]any{"script": "exit 1", "executor_type": "agent"},
			stub:          stubExecutor{output: "boom", err: errors.New("exit status 1")},
			expectErr:     true,
			expectedError: "script execution failed: exit status 1",
		},
		{
			name: "Script Using Environment Variable",
			params: map[string]any{
				"script":        "echo $MY_TEST_VAR",
				"executor_type": "agent",
				"env":           map[string]string{"MY_TEST_VAR": "EnvValue"},
			},
			stub:        stubExecutor{output: "EnvValue"},
			expected:    "EnvValue",
			expectedEnv: map[string]string{"MY_TEST_VAR": "EnvValue"},
		},
		{
			name:     "JSON Output Parsed",
			params:   map[string]any{"script": "echo '{\"a\":1}'", "executor_type": "agent", "parser_type": "json"},
			stub:     stubExecutor{output: `{"a":1}`},
			expected: map[string]any{"a": float64(1)},
		},
		{
			name: "Missing Script",
			params: map[string]any{
				"executor_type": "agent",
				"env":           map[string]string{"VAR": "VALUE"},
			},
			expectErr:     true,
			expectedError: "missing required parameter: 'script'",
		},
		{
			name:          "AWS SSM Missing target_id",
			params:        map[string]any{"script": "echo test", "executor_type": "aws_ssm", "region": "us-east-1"},
			expectErr:     true,
			expectedError: "target_id is required for aws_ssm executor",
		},
		{
			name:          "AWS SSM Missing region",
			params:        map[string]any{"script": "echo test", "executor_type": "aws_ssm", "target_id": "i-12345"},
			expectErr:     true,
			expectedError: "region is required for aws_ssm executor",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stub := tc.stub
			useStubExecutor(t, &stub)

			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" && err != nil {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
				return
			}
			if assert.NoError(t, err) && result != nil {
				assert.Equal(t, tc.expected, result.(map[string]any)["data"])
			}
			for k, v := range tc.expectedEnv {
				assert.Equal(t, v, stub.got.Env[k])
			}
		})
	}
}

// Scripts never run inside the runbook-server process: the old "local"
// executor is gone, and naming it fails instead of running anything.
func TestRunScriptTask_RefusesLocalExecutor(t *testing.T) {
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())
	_, err := (&RunScriptTask{}).Execute(taskCtx, map[string]any{
		"script":        "echo hi",
		"executor_type": "local",
	})
	assert.ErrorContains(t, err, "unknown script execution mode: local")
}

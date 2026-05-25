package executors_test

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK8sAgentExecutor_Execute(t *testing.T) {
	t.Run("Bash: Simple Echo", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor := executors.NewAgentExecutor()
		config := executors.ExecutionConfig{
			Script:   "echo 'Hello World'",
			Language: "bash",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello World", output)
	})

	t.Run("Python: Simple Echo", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor := executors.NewAgentExecutor()
		config := executors.ExecutionConfig{
			Script:   "print('Hello World')",
			Language: "python",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello World", output)
	})

	t.Run("PowerShell: Simple Write-Output", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor := executors.NewAgentExecutor()
		config := executors.ExecutionConfig{
			Script:   "Write-Output 'Hello PowerShell'",
			Language: "powershell",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello PowerShell", output)
	})
}

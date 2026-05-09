package executors_test

import (
	"context"
	"log/slog"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK8sExecutor_Execute(t *testing.T) {
	t.Run("Bash: Simple Echo", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		assert.NoError(t, err)
		config := executors.ExecutionConfig{
			Script:   "echo 'Hello World'",
			Language: "bash",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello World", output)
	})

	t.Run("Bash: Timeout/Context Cancellation", func(t *testing.T) {
		// Create a context that cancels quickly
		timeoutCtx, cancel := context.WithTimeout(context.TODO(), 2*time.Second)
		defer cancel()

		ctx := testutils.NewTestTaskContextWithContext(timeoutCtx, os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		require.NoError(t, err)

		config := executors.ExecutionConfig{
			Script:   "sleep 10; echo 'Should not see this'",
			Language: "bash",
		}

		_, err = executor.Execute(ctx, config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("Bash: Environment Variables", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		require.NoError(t, err)
		config := executors.ExecutionConfig{
			Script:   "echo $TEST_VAR",
			Language: "bash",
			Env:      map[string]string{"TEST_VAR": "foo"},
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "foo", output)
	})

	t.Run("Javascript: Simple Log", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		require.NoError(t, err)

		config := executors.ExecutionConfig{
			Script:   "console.log('Hello Node');",
			Language: "javascript",
			K8sImage: "node:current-alpine", // Use a known node image for this test
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello Node", output)
	})

	t.Run("Python: Simple Log", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		require.NoError(t, err)

		config := executors.ExecutionConfig{
			Script:   "print('Hello Python')",
			Language: "python",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello Python", output)
	})

	t.Run("PowerShell: Simple Write-Output", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		require.NoError(t, err)

		config := executors.ExecutionConfig{
			Script:   "Write-Output 'Hello PowerShell'",
			Language: "powershell",
			K8sImage: "mcr.microsoft.com/powershell:lts-alpine-3.17",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello PowerShell", output)
	})

	t.Run("Sh: Simple Echo with Alpine image (no bash)", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		require.NoError(t, err)

		config := executors.ExecutionConfig{
			Script:   "echo 'Hello sh'",
			Language: "sh",
			K8sImage: "alpine:3.19", // Alpine has sh but NOT bash — validates the fix
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello sh", output)
	})

	t.Run("Unsupported Language", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		require.NoError(t, err)
		config := executors.ExecutionConfig{
			Script:   "echo test",
			Language: "java",
		}
		_, err = executor.Execute(ctx, config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported language")
	})

	t.Run("Error Execution", func(t *testing.T) {
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor, err := executors.NewKubernetesExecutor()
		require.NoError(t, err)
		config := executors.ExecutionConfig{
			Script:   "exit 1",
			Language: "bash",
		}
		_, err = executor.Execute(ctx, config)
		require.Error(t, err)
	})
}

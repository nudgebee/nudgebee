package executors_test

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwsSsmExecutor_Execute(t *testing.T) {
	targetId := os.Getenv("TEST_AWS_SSM_TARGET_ID")
	region := os.Getenv("TEST_AWS_SSM_REGION")
	if targetId == "" || region == "" {
		t.Skip("Skipping AWS SSM test: TEST_AWS_SSM_TARGET_ID and TEST_AWS_SSM_REGION must be set")
	}
	vmOS := os.Getenv("TEST_AWS_SSM_OS")
	if vmOS == "" {
		vmOS = "linux"
	}

	t.Run("Bash: Simple Echo", func(t *testing.T) {
		if strings.ToLower(vmOS) == "windows" {
			t.Skip("Skipping Bash test on Windows VM")
		}
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor := executors.NewAwsSsmExecutor()
		config := executors.ExecutionConfig{
			Script:   "echo 'Hello World'",
			Language: "bash",
			TargetID: targetId,
			Region:   region,
			OSType:   vmOS,
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello World", strings.TrimSpace(output))
	})

	t.Run("Python: Simple Echo", func(t *testing.T) {
		if strings.ToLower(vmOS) == "windows" {
			t.Skip("Skipping Python test on Windows VM")
		}
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor := executors.NewAwsSsmExecutor()
		config := executors.ExecutionConfig{
			Script:   "print('Hello World')",
			Language: "python",
			TargetID: targetId,
			Region:   region,
			OSType:   vmOS,
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello World", strings.TrimSpace(output))
	})

	t.Run("PowerShell: Simple Write-Output", func(t *testing.T) {
		if strings.ToLower(vmOS) == "linux" {
			t.Skip("Skipping PowerShell test on Linux VM")
		}
		ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

		executor := executors.NewAwsSsmExecutor()
		config := executors.ExecutionConfig{
			Script:   "Write-Output 'Hello PowerShell'",
			Language: "powershell",
			TargetID: targetId,
			Region:   region,
			OSType:   vmOS,
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello PowerShell", strings.TrimSpace(output))
	})
}

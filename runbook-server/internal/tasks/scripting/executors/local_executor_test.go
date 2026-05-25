package executors_test

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/scripting/executors"

	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalExecutor_Execute(t *testing.T) {
	executor := executors.NewLocalExecutor()
	ctx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	t.Run("Bash: Simple Echo", func(t *testing.T) {
		config := executors.ExecutionConfig{
			Script:   "echo 'Hello World'",
			Language: "bash",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello World", output)
	})

	t.Run("Bash: Environment Variables", func(t *testing.T) {
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
		// Check if node is available
		if _, err := execLookPath("node"); err != nil {
			t.Skip("node executable not found")
		}

		config := executors.ExecutionConfig{
			Script:   "console.log('Hello Node');",
			Language: "javascript",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello Node", output)
	})

	t.Run("Python: Simple Log", func(t *testing.T) {
		// Check if python3 is available
		if _, err := execLookPath("python3"); err != nil {
			t.Skip("python3 executable not found")
		}

		config := executors.ExecutionConfig{
			Script:   "print('Hello Python')",
			Language: "python",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello Python", output)
	})

	t.Run("Bash: With Arguments", func(t *testing.T) {
		config := executors.ExecutionConfig{
			Script:   "echo $1 $2",
			Language: "bash",
			Args:     []string{"foo", "bar"},
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "foo bar", output)
	})

	t.Run("Python: With Arguments", func(t *testing.T) {
		if _, err := execLookPath("python3"); err != nil {
			t.Skip("python3 executable not found")
		}
		config := executors.ExecutionConfig{
			Script:   "import sys; print(sys.argv[1] + ' ' + sys.argv[2])",
			Language: "python",
			Args:     []string{"foo", "bar"},
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "foo bar", output)
	})

	t.Run("PowerShell: Simple Write-Output", func(t *testing.T) {
		if _, err := execLookPath("pwsh"); err != nil {
			t.Skip("pwsh executable not found")
		}

		config := executors.ExecutionConfig{
			Script:   "Write-Output 'Hello PowerShell'",
			Language: "powershell",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "Hello PowerShell", output)
	})

	t.Run("PowerShell: Environment Variables", func(t *testing.T) {
		if _, err := execLookPath("pwsh"); err != nil {
			t.Skip("pwsh executable not found")
		}

		config := executors.ExecutionConfig{
			Script:   "Write-Output $env:TEST_VAR",
			Language: "powershell",
			Env:      map[string]string{"TEST_VAR": "foo"},
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "foo", output)
	})

	t.Run("PowerShell: With Arguments", func(t *testing.T) {
		if _, err := execLookPath("pwsh"); err != nil {
			t.Skip("pwsh executable not found")
		}

		config := executors.ExecutionConfig{
			Script:   "param($a, $b)\nWrite-Output \"$a $b\"",
			Language: "powershell",
			Args:     []string{"-a", "foo", "-b", "bar"},
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)
		assert.Equal(t, "foo bar", output)
	})

	t.Run("Unsupported Language", func(t *testing.T) {
		config := executors.ExecutionConfig{
			Script:   "echo test",
			Language: "ruby",
		}
		_, err := executor.Execute(ctx, config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported language")
	})

	t.Run("Error Execution", func(t *testing.T) {
		config := executors.ExecutionConfig{
			Script:   "exit 1",
			Language: "bash",
		}
		_, err := executor.Execute(ctx, config)
		require.Error(t, err)
	})

	t.Run("Security: Host Env Isolation", func(t *testing.T) {
		// Set a sensitive environment variable on the host
		sensitiveKey := "SENSITIVE_SECRET_KEY"
		sensitiveValue := "DO_NOT_LEAK"
		t.Setenv(sensitiveKey, sensitiveValue)

		// Try to access it from the script
		config := executors.ExecutionConfig{
			Script:   "echo $" + sensitiveKey,
			Language: "bash",
		}
		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)

		// Assert that the output is EMPTY, meaning the env var was NOT accessible
		assert.Equal(t, "", output, "Host environment variable was leaked to the script!")
	})

	t.Run("Security: Empty Safe Env (Nil Slice Check)", func(t *testing.T) {
		// Clear all safe keys from the environment for this test
		safeKeys := []string{
			"PATH", "HOME", "LANG", "USER", "SHELL", "TERM", "TMPDIR", "TZ",
			"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
			"http_proxy", "https_proxy", "no_proxy",
		}
		for _, key := range safeKeys {
			t.Setenv(key, "")
		}

		// Set a sensitive variable
		t.Setenv("SENSITIVE_SECRET_KEY_2", "LEAK_CHECK")

		// Execute
		config := executors.ExecutionConfig{
			Script:   "echo $SENSITIVE_SECRET_KEY_2",
			Language: "bash",
		}

		output, _ := executor.Execute(ctx, config)

		// Even if execution fails (due to missing PATH), we want to make sure we didn't crash or leak.
		assert.NotContains(t, output, "LEAK_CHECK")
	})

	t.Run("Security: Output Size Limit", func(t *testing.T) {
		if _, err := execLookPath("python3"); err != nil {
			t.Skip("python3 executable not found")
		}

		// Generate 2MB of output
		script := "print('a' * 2 * 1024 * 1024)"
		config := executors.ExecutionConfig{
			Script:   script,
			Language: "python",
		}

		output, err := executor.Execute(ctx, config)
		require.NoError(t, err)

		// Verify that the output IS truncated
		const maxOutputSize = 1024 * 1024
		// Allow small buffer for the truncation message
		assert.Less(t, len(output), maxOutputSize+200, "Output should be limited to ~1MB")
		assert.Greater(t, len(output), maxOutputSize, "Output should be at least 1MB")
		assert.True(t, strings.Contains(output, "... output truncated"), "Output should contain truncation message")
	})
}

func TestFactory_DefaultsToAgent(t *testing.T) {
	// Ensure no env var is set for this test
	t.Setenv("RUNBOOK_SERVER_TASK_SCRIPTING_MODE", "")
	executor, err := executors.NewExecutor(executors.ExecutionConfig{})
	require.NoError(t, err)
	assert.IsType(t, &executors.AgentExecutor{}, executor)
}

func TestFactory_Kubernetes(t *testing.T) {
	_ = os.Setenv("RUNBOOK_SERVER_TASK_SCRIPTING_MODE", "kubernetes")
	_ = os.Setenv("KUBECONFIG", "/non/existent/path") // Ensure loading config fails
	defer func() {
		_ = os.Unsetenv("RUNBOOK_SERVER_TASK_SCRIPTING_MODE")
		_ = os.Unsetenv("KUBECONFIG")
	}()

	// This should fail if we are not in a cluster because NewKubernetesExecutor calls InClusterConfig
	_, err := executors.NewExecutor(executors.ExecutionConfig{})
	// It's expected to fail here because we are not in a k8s pod
	if err != nil {
		assert.Contains(t, err.Error(), "failed to load kubernetes config")
	}
}

func TestFactory_AwsSsm(t *testing.T) {
	executor, err := executors.NewExecutor(executors.ExecutionConfig{ExecutorType: "aws_ssm"})
	require.NoError(t, err)
	assert.IsType(t, &executors.AwsSsmExecutor{}, executor)
}

// Helper to check if executable exists in path
func execLookPath(file string) (string, error) {
	return exec.LookPath(file)
}

package scripting

import (
	"context"
	"errors"
	"testing"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func stubAccountProvider(t *testing.T, provider string) {
	t.Helper()
	original := getAccountProvider
	getAccountProvider = func(_ *security.RequestContext, _ string) (string, error) { return provider, nil }
	t.Cleanup(func() { getAccountProvider = original })
}

// A workflow saved before the save-time check still reaches Execute. It must
// fail once, naming the fix — before any executor (or cluster client) is built,
// and without Temporal retrying a pairing that can never work.
func TestRunScriptTask_RefusesAccountExecutorMismatch(t *testing.T) {
	stubAccountProvider(t, "K8s")
	_, err := (&RunScriptTask{}).Execute(testTaskContext(context.Background()), map[string]any{
		"script":        "kubectl get pods",
		"language":      "bash",
		"executor_type": "kubernetes",
		"account_id":    "cluster-account",
	})
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "want a Temporal application error, got %T", err)
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, "AccountExecutorMismatch", appErr.Type())
	assert.Contains(t, err.Error(), "set Executor Type to Agent")
}

func TestResolveExecutorType(t *testing.T) {
	original := config.Config.TaskScriptExecutionModel
	t.Cleanup(func() { config.Config.TaskScriptExecutionModel = original })
	config.Config.TaskScriptExecutionModel = ""

	for _, tc := range []struct {
		explicit, provider, want string
	}{
		{"Kubernetes", "K8s", "kubernetes"}, // explicit wins, even when it cannot work
		{"", "K8s", "agent"},
		{"", "AWS", "kubernetes"},
		{"", "GCP", "kubernetes"},
		{"", "SelfHosted", "agent"},
		{"", "", "agent"},
	} {
		got := executors.ResolveExecutorType(executors.ExecutionConfig{ExecutorType: tc.explicit, AccountProvider: tc.provider})
		assert.Equal(t, tc.want, got, "explicit %q, provider %q", tc.explicit, tc.provider)
	}

	config.Config.TaskScriptExecutionModel = "local"
	assert.Equal(t, "local", executors.ResolveExecutorType(executors.ExecutionConfig{AccountProvider: "SelfHosted"}),
		"an unrecognised provider falls back to the system default")
}

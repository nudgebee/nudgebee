package executors

import (
	"nudgebee/runbook/config"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A script step's account has to be one its executor can run for; the error
// names the change that makes the step work.
func TestCheckAccountSupports(t *testing.T) {
	for _, tc := range []struct {
		executor, provider string
		want               string // substring of the error; "" means the pair works
	}{
		// The pair that broke saved workflows: no cloud credentials to mint.
		{"kubernetes", "K8s", "set Executor Type to Agent"},
		{"Kubernetes", "kubernetes", "set Executor Type to Agent"},
		{"aws_ssm", "K8s", "needs an AWS account"},
		{"azure_run_command", "K8s", "needs an Azure account"},
		{"gcp_compute_ssh", "K8s", "needs a GCP account"},
		{"kubernetes", "SelfHosted", "a SelfHosted account has no cloud credentials"},
		{"aws_ssm", "Azure", "needs an AWS account, and this step's account is Azure"},
		{"gcp_compute_ssh", "AWS", "needs a GCP account"},

		{"kubernetes", "AWS", ""},
		{"kubernetes", "Azure", ""},
		{"kubernetes", "GCP", ""},
		{"kubernetes", "gcloud", ""},
		{"aws_ssm", "AWS", ""},
		{"azure_run_command", "Azure", ""},
		{"gcp_compute_ssh", "GCP", ""},
		{"agent", "K8s", ""},
		{"ssh", "K8s", ""},
		// Nothing to judge: no executor named, no account, or an executor
		// NewExecutor itself will reject.
		{"", "K8s", ""},
		{"kubernetes", "", ""},
		{"teleport", "K8s", ""},
	} {
		err := CheckAccountSupports(tc.executor, tc.provider)
		if tc.want == "" {
			assert.NoError(t, err, "%s on %s", tc.executor, tc.provider)
			continue
		}
		if assert.Error(t, err, "%s on %s", tc.executor, tc.provider) {
			assert.Contains(t, err.Error(), tc.want)
		}
	}
}

func TestFactory_DefaultsToAgent(t *testing.T) {
	t.Setenv("RUNBOOK_SERVER_TASK_SCRIPTING_MODE", "")
	executor, err := NewExecutor(ExecutionConfig{})
	require.NoError(t, err)
	assert.IsType(t, &AgentExecutor{}, executor)
}

func TestFactory_Kubernetes(t *testing.T) {
	t.Setenv("KUBECONFIG", "/non/existent/path") // Ensure loading config fails

	// Outside a cluster there is no config to load; inside one, in-cluster
	// config succeeds. Either way the factory must build the Kubernetes executor.
	executor, err := NewExecutor(ExecutionConfig{ExecutorType: "kubernetes"})
	if err != nil {
		assert.Contains(t, err.Error(), "failed to load kubernetes config")
	} else {
		assert.IsType(t, &KubernetesExecutor{}, executor)
	}
}

func TestFactory_AwsSsm(t *testing.T) {
	executor, err := NewExecutor(ExecutionConfig{ExecutorType: "aws_ssm"})
	require.NoError(t, err)
	assert.IsType(t, &AwsSsmExecutor{}, executor)
}

// Scripts never run inside the runbook-server process, whether a step names
// "local" or the system default is set to it.
func TestFactory_RefusesLocal(t *testing.T) {
	_, err := NewExecutor(ExecutionConfig{ExecutorType: "local"})
	assert.ErrorContains(t, err, "unknown script execution mode: local")

	original := config.Config.TaskScriptExecutionModel
	t.Cleanup(func() { config.Config.TaskScriptExecutionModel = original })
	config.Config.TaskScriptExecutionModel = "local"
	_, err = NewExecutor(ExecutionConfig{})
	assert.ErrorContains(t, err, "unknown script execution mode: local")
}

package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/tools/core"
)

func TestExecutionToolsPrepareDelegatedTargetBeforeApproval(t *testing.T) {
	tests := []struct {
		name     string
		tool     core.DelegatedTargetInputPreparer
		provider string
		input    string
		explicit string
		field    string
	}{
		{"aws", AwsCliTool{}, "aws", `{"command":"aws ec2 terminate-instances --instance-ids i-1"}`, `{"command":"aws ec2 terminate-instances --instance-ids i-1","account":"other"}`, "account"},
		{"gcp", GcpCliTool{}, "gcp", `{"command":"gcloud compute instances delete vm"}`, `{"command":"gcloud compute instances delete vm","account":"other"}`, "account"},
		{"azure", AzureCliTool{}, "azure", `{"command":"az vm delete --name vm"}`, `{"command":"az vm delete --name vm","account":"other"}`, "account"},
		{"kubectl", KubectlExecuteTool{}, "kubernetes", `{"command":"kubectl delete pod x"}`, `{"command":"kubectl --context other delete pod x"}`, "cluster"},
		{"helm", HelmExecuteTool{}, "kubernetes", `{"command":"helm uninstall app"}`, `{"command":"helm --kube-context other uninstall app"}`, "cluster"},
		{"resource search", K8sResourceSearchTool{}, "kubernetes", `{"query":"pods"}`, `{"query":"pods","cluster":"other"}`, "cluster"},
		{"shell aws", ShellTool{}, "aws", `{"command":"aws ec2 terminate-instances --instance-ids i-1"}`, `{"command":"aws --profile other ec2 terminate-instances --instance-ids i-1"}`, "account"},
		{"shell kubectl", ShellTool{}, "kubernetes", `{"command":"kubectl delete pod x"}`, `{"command":"kubectl --context other delete pod x"}`, "cluster"},
		{"shell helm", ShellTool{}, "kubernetes", `{"command":"helm uninstall app"}`, `{"command":"helm --kube-context other uninstall app"}`, "cluster"},
		{"shell gcp", ShellTool{}, "gcp", `{"command":"gcloud compute instances delete vm"}`, `{"command":"gcloud compute instances delete vm","account":"other"}`, "account"},
		{"shell azure", ShellTool{}, "azure", `{"command":"az vm delete --name vm"}`, `{"command":"az vm delete --name vm","account":"other"}`, "account"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &core.ExecutionTarget{Provider: tt.provider, Name: "delegated"}
			prepared, err := tt.tool.PrepareDelegatedTargetInput(target, tt.input)
			require.NoError(t, err)
			req := core.ParseNBToolCallRequestFromInput(prepared)
			require.Equal(t, "delegated", req.Arguments[tt.field])

			prepared, err = tt.tool.PrepareDelegatedTargetInput(target, tt.explicit)
			require.NoError(t, err)
			require.Equal(t, tt.explicit, prepared, "explicit per-call selection must win")

			prepared, err = tt.tool.PrepareDelegatedTargetInput(&core.ExecutionTarget{Provider: "unrelated", Name: "wrong"}, tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.input, prepared)
		})
	}
}

func TestTargetedShellWriteRequiresOneVisibleRemoteCommand(t *testing.T) {
	shell := ShellTool{}
	for _, command := range []string{
		"python3 -c 'import subprocess; subprocess.run(\"aws s3 rm s3://bucket/key\")'",
		"aws s3 rm s3://bucket/key && az vm delete --name vm",
		"kubectl delete pod x | cat",
		"echo deleting; aws s3 rm s3://bucket/key",
	} {
		require.Error(t, shell.ValidateWriteTargetInput(`{"command":`+mustJSON(t, command)+`}`), command)
	}
	for _, command := range []string{
		"aws s3 rm s3://bucket/key",
		"kubectl delete pod x",
		"helm uninstall app",
		"az vm delete --name vm",
	} {
		require.NoError(t, shell.ValidateWriteTargetInput(`{"command":`+mustJSON(t, command)+`}`), command)
	}
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func TestAddDelegatedTargetObject(t *testing.T) {
	target := &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod"}

	// JSON object without target
	out, err := AddDelegatedTargetObject(`{"query":"rate(http_requests_total[5m])"}`, target)
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Equal(t, "rate(http_requests_total[5m])", parsed["query"])
	targetObj, ok := parsed["target"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "kubernetes", targetObj["provider"])
	require.Equal(t, "k8s-prod", targetObj["name"])

	// Explicit target already present - should not be overridden
	out, err = AddDelegatedTargetObject(`{"query":"rate(http_requests_total[5m])","target":{"provider":"aws","name":"my-cluster"}}`, target)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	targetObj = parsed["target"].(map[string]any)
	require.Equal(t, "aws", targetObj["provider"])
	require.Equal(t, "my-cluster", targetObj["name"])

	// Plain text command
	out, err = AddDelegatedTargetObject("show CPU usage", target)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Equal(t, "show CPU usage", parsed["command"])
	targetObj = parsed["target"].(map[string]any)
	require.Equal(t, "kubernetes", targetObj["provider"])
	require.Equal(t, "k8s-prod", targetObj["name"])

	// Empty input
	out, err = AddDelegatedTargetObject("", target)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	targetObj = parsed["target"].(map[string]any)
	require.Equal(t, "kubernetes", targetObj["provider"])
	require.Equal(t, "k8s-prod", targetObj["name"])

	// Nil target returns input untouched
	out, err = AddDelegatedTargetObject(`{"query":"up"}`, nil)
	require.NoError(t, err)
	require.Equal(t, `{"query":"up"}`, out)
}

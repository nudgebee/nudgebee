package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestedKubernetesCluster(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    map[string]any
		want    string
		wantErr string
	}{
		{name: "nothing requested", command: "kubectl get pods -A"},
		{name: "cluster argument", command: "kubectl get pods", args: map[string]any{"cluster": " k8s-dev "}, want: "k8s-dev"},
		{name: "context flag", command: "kubectl --context k8s-dev get pods", want: "k8s-dev"},
		{name: "context flag with equals", command: "kubectl get pods --context=k8s-dev -A", want: "k8s-dev"},
		{name: "quoted context", command: `kubectl get pods --context "k8s-dev"`, want: "k8s-dev"},
		{name: "same cluster twice", command: "kubectl --context k8s-dev get ns && kubectl --context K8S-DEV get pods", want: "k8s-dev"},
		{name: "argument and flag agree", command: "kubectl --context k8s-dev get pods", args: map[string]any{"cluster": "K8s-Dev"}, want: "K8s-Dev"},
		{name: "helm kube-context is not a kubectl context", command: "kubectl get pods; helm --kube-context other list"},
		{name: "two clusters in one command", command: "kubectl --context a get pods && kubectl --context b get pods", wantErr: "two clusters"},
		{name: "argument and flag disagree", command: "kubectl --context a get pods", args: map[string]any{"cluster": "b"}, wantErr: "disagree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RequestedKubernetesCluster(tc.command, tc.args)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestStripKubectlContextFlag(t *testing.T) {
	require.Equal(t, "kubectl get pods -A | grep api", StripKubectlContextFlag("kubectl --context k8s-dev get pods -A | grep api"))
	require.Equal(t, "kubectl get pods -A", StripKubectlContextFlag("kubectl get pods --context=k8s-dev -A"))
	require.Equal(t, "kubectl get pods", StripKubectlContextFlag(`kubectl get pods --context 'k8s-dev'`))
	require.Equal(t, "helm --kube-context x list", StripKubectlContextFlag("helm --kube-context x list"))
}

// clusterFixture caches three clusters for account; the caller can read only
// the accounts passed to the request context.
//
// It mirrors production: the per-account list is narrowed to the conversation's
// own cluster (k8s-prod), while the tenant list holds every cluster.
func clusterFixture(t *testing.T, account string) {
	t.Helper()
	prod := core.ToolConfig{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "prod-id"}}}
	cacheShellTargets(t, account, ToolExecuteKubectlCommand, []core.ToolConfig{prod})
	cacheTenantClusters(t, "tenant", []core.ToolConfig{
		prod,
		{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: "dev-id"}}},
		{Name: "k8s-dev-oss", Values: []core.ToolConfigValue{{Name: "id", Value: "oss-id"}}},
		{Name: "k8s-secret", Values: []core.ToolConfigValue{{Name: "id", Value: "no-access-id"}}},
	})
}

func cacheTenantClusters(t *testing.T, tenant string, configs []core.ToolConfig) {
	t.Helper()
	StubTenantClusters(t, tenant, configs)
}

func clusterCtx(account string) *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "prod-id", "dev-id", "oss-id"})
}

func TestKubernetesTargetByName(t *testing.T) {
	const account = "cluster-by-name"
	clusterFixture(t, account)
	ctx := clusterCtx(account)

	got, err := KubernetesTargetByName(ctx, account, "K8S-DEV")
	require.NoError(t, err)
	require.Equal(t, "k8s-dev", got.Name, "exact match, not the k8s-dev-oss prefix sibling")

	_, err = KubernetesTargetByName(ctx, account, "k8s-secret")
	require.ErrorContains(t, err, "not connected or you do not have access")

	_, err = KubernetesTargetByName(ctx, account, "gke_nudgebee-dev_us-central1_nudgebee-dev")
	require.ErrorContains(t, err, "available clusters: k8s-dev, k8s-dev-oss, k8s-prod")
	require.NotContains(t, err.Error(), "k8s-secret", "clusters the caller cannot read are not listed")
}

func kubectlCallCtx(t *testing.T, account string, tool core.NBTool) core.NbToolContext {
	t.Helper()
	return core.NewNbToolContext(clusterCtx(account), tool, account, "user", "conversation", "message", "agent", "", nil, "",
		core.NBQueryConfig{ToolConfigs: map[string]string{ToolExecuteKubectlCommand: "k8s-prod"}}, "call")
}

func TestKubectlExecutePerCallCluster(t *testing.T) {
	const account = "kubectl-per-call"
	clusterFixture(t, account)

	t.Run("no override stays on the saved cluster", func(t *testing.T) {
		capture := &targetWorkspaceCapture{}
		tool := KubectlExecuteTool{workspaceManager: capture}
		resp, err := tool.Call(kubectlCallCtx(t, account, tool), core.NBToolCallRequest{Command: "kubectl get pods -A"})
		require.NoError(t, err)
		require.Equal(t, "k8s-prod", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
		require.Equal(t, "prod-id", capture.env[workspace.ENV_NB_TARGET_ACCOUNT_ID])
		require.NotContains(t, resp.Data, `"cluster"`)
	})

	t.Run("cluster argument targets that cluster for this call only", func(t *testing.T) {
		capture := &targetWorkspaceCapture{}
		tool := KubectlExecuteTool{workspaceManager: capture}
		ctx := kubectlCallCtx(t, account, tool)
		resp, err := tool.Call(ctx, core.NBToolCallRequest{Command: "kubectl get pods -A", Arguments: map[string]any{"cluster": "k8s-dev"}})
		require.NoError(t, err)
		require.Equal(t, "k8s-dev", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
		require.Equal(t, "dev-id", capture.env[workspace.ENV_NB_TARGET_ACCOUNT_ID], "the execution token is bound to the selected cluster")
		require.Equal(t, account, capture.account, "the conversation workspace does not move")
		require.Equal(t, "conversation", capture.conversation)
		out := map[string]string{}
		require.NoError(t, common.UnmarshalJson([]byte(resp.Data), &out))
		require.Equal(t, "k8s-dev", out["cluster"])
		require.Contains(t, resp.References[0].Url, "/kubernetes/details/dev-id")
		require.Equal(t, "k8s-prod", ctx.QueryConfig.ToolConfigs[ToolExecuteKubectlCommand], "saved selection unchanged")
	})

	t.Run("context flag targets that cluster and is removed", func(t *testing.T) {
		capture := &targetWorkspaceCapture{}
		tool := KubectlExecuteTool{workspaceManager: capture}
		_, err := tool.Call(kubectlCallCtx(t, account, tool), core.NBToolCallRequest{Command: "kubectl --context k8s-dev get pods -n nudgebee"})
		require.NoError(t, err)
		require.Equal(t, "k8s-dev", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
		require.Equal(t, "kubectl get pods -n nudgebee", capture.command)
	})

	t.Run("unknown cluster fails with the available names and runs nothing", func(t *testing.T) {
		capture := &targetWorkspaceCapture{}
		tool := KubectlExecuteTool{workspaceManager: capture}
		resp, err := tool.Call(kubectlCallCtx(t, account, tool), core.NBToolCallRequest{Command: "kubectl get pods", Arguments: map[string]any{"cluster": "nope"}})
		require.Error(t, err)
		require.Contains(t, resp.Data, "available clusters: k8s-dev, k8s-dev-oss, k8s-prod")
		require.Empty(t, capture.command)
	})

	t.Run("conflicting targets fail and run nothing", func(t *testing.T) {
		capture := &targetWorkspaceCapture{}
		tool := KubectlExecuteTool{workspaceManager: capture}
		_, err := tool.Call(kubectlCallCtx(t, account, tool), core.NBToolCallRequest{Command: "kubectl --context k8s-prod get pods", Arguments: map[string]any{"cluster": "k8s-dev"}})
		require.ErrorContains(t, err, "disagree")
		require.Empty(t, capture.command)
	})
}

func TestShellKubectlPerCallCluster(t *testing.T) {
	const account = "shell-per-call"
	clusterFixture(t, account)

	capture := &targetWorkspaceCapture{}
	tool := ShellTool{AccountId: account, workspaceManager: capture}
	ctx := kubectlCallCtx(t, account, tool)
	_, err := tool.Call(ctx, core.NBToolCallRequest{Command: "kubectl --context k8s-dev get pods -A > dev_pods.txt"})
	require.NoError(t, err)
	require.Equal(t, "k8s-prod", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME], "saved context remains the default")
	require.Equal(t, account, capture.account)
	require.Equal(t, "kubectl --context k8s-dev get pods -A > dev_pods.txt", capture.command, "the concrete shim resolves and strips context")

	capture = &targetWorkspaceCapture{}
	tool.workspaceManager = capture
	_, err = tool.Call(ctx, core.NBToolCallRequest{Command: "kubectl --context k8s-secret get pods"})
	require.NoError(t, err, "outer shell does not guess the concrete target")
	require.Equal(t, "kubectl --context k8s-secret get pods", capture.command)
}

func TestShellKubectlVisibleWriteKeepsTargetBoundToken(t *testing.T) {
	const account = "shell-write-per-call"
	clusterFixture(t, account)

	capture := &targetWorkspaceCapture{}
	tool := ShellTool{AccountId: account, workspaceManager: capture}
	ctx := kubectlCallCtx(t, account, tool)
	_, err := tool.Call(ctx, core.NBToolCallRequest{Command: "kubectl --context k8s-dev delete pod api"})
	require.NoError(t, err)
	require.Equal(t, "k8s-dev", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
	require.Equal(t, "dev-id", capture.env[workspace.ENV_NB_TARGET_ACCOUNT_ID])
}

func TestDirectShellKubectlTypeFailsSecureForCompoundCommands(t *testing.T) {
	require.Equal(t, core.ToolRequestTypeRead, directShellKubectlType("kubectl get pods"))
	require.Equal(t, core.ToolRequestTypeDelete, directShellKubectlType("kubectl delete pod api"))
	require.Empty(t, directShellKubectlType("kubectl get pods;kubectl delete pod api"))
	require.Empty(t, directShellKubectlType("kubectl get pods|kubectl delete pod api"))
	require.Empty(t, directShellKubectlType("kubectl get pods && kubectl delete pod api"))
}

func TestKubernetesConfirmationKeyIsPerCluster(t *testing.T) {
	kubectl := KubectlExecuteTool{}
	require.Equal(t, "", kubectl.ConfirmationKey(`{"command":"kubectl delete pod x"}`), "no explicit cluster keeps the per-tool key")
	dev := kubectl.ConfirmationKey(`{"command":"kubectl delete pod x","cluster":"k8s-dev"}`)
	prod := kubectl.ConfirmationKey(`{"command":"kubectl delete pod x","cluster":"k8s-prod"}`)
	require.Equal(t, "kubectl_execute@k8s-dev", dev)
	require.NotEqual(t, dev, prod)
	require.Equal(t, dev, kubectl.ConfirmationKey(`kubectl --context K8S-DEV delete pod x`))
	require.NotEqual(t, "", kubectl.ConfirmationKey(`{"command":"kubectl --context a delete pod x","cluster":"b"}`))

	shell := ShellTool{}
	require.Equal(t, "", shell.ConfirmationKey(`{"command":"kubectl delete pod x"}`))
	require.Equal(t, "", shell.ConfirmationKey(`{"command":"aws s3 rm s3://b/k --context x"}`), "non-kubectl commands keep the per-tool key")
	require.Equal(t, "shell_execute@k8s-dev", shell.ConfirmationKey(`{"command":"kubectl --context k8s-dev delete pod x"}`))
}

func TestKubectlIdentifyConfigPrefersExplicitCluster(t *testing.T) {
	configs := []core.ToolConfig{
		{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "prod-id"}}},
		{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: "dev-id"}}},
		{Name: "k8s-dev-oss", Values: []core.ToolConfigValue{{Name: "id", Value: "oss-id"}}},
	}
	ctx := core.NbToolContext{QueryConfig: core.NBQueryConfig{Labels: map[string]any{"nb_cloud_account_id": "prod-id"}}}
	tool := KubectlExecuteTool{}

	got, err := tool.IdentifyConfig(ctx, core.NBToolCallRequest{Command: `{"command":"kubectl get pods","cluster":"k8s-dev"}`}, configs)
	require.NoError(t, err)
	require.Equal(t, "k8s-dev", got.Name, "explicit cluster beats the UI page label and the k8s-dev-oss substring")

	got, err = tool.IdentifyConfig(ctx, core.NBToolCallRequest{Command: `{"command":"kubectl --context k8s-dev get pods"}`}, configs)
	require.NoError(t, err)
	require.Equal(t, "k8s-dev", got.Name)

	got, err = tool.IdentifyConfig(ctx, core.NBToolCallRequest{Command: "kubectl get pods --context=k8s-dev"}, configs)
	require.NoError(t, err)
	require.Equal(t, "k8s-dev", got.Name, "a raw (non-JSON) command is parsed too")

	got, err = tool.IdentifyConfig(ctx, core.NBToolCallRequest{Command: `{"command":"kubectl get pods"}`}, configs)
	require.NoError(t, err)
	require.Equal(t, "k8s-prod", got.Name, "without an explicit cluster the page label still decides")
}

func TestStripKubectlContextFlagKeepsScriptLines(t *testing.T) {
	require.Equal(t, "kubectl get ns\nkubectl get pods", StripKubectlContextFlag("kubectl --context k8s-dev get ns\nkubectl get pods --context k8s-dev"))
}

func TestKubectlContextFlagIgnoresQuotedAndRemoteText(t *testing.T) {
	cases := []struct {
		name, command, wantCluster, wantStripped string
	}{
		{name: "literal inside a quoted script", command: `kubectl exec mypod -- sh -c "echo --context production"`, wantStripped: `kubectl exec mypod -- sh -c "echo --context production"`},
		{name: "remote command after --", command: "kubectl exec mypod -- kubectl --context production get pods", wantStripped: "kubectl exec mypod -- kubectl --context production get pods"},
		{name: "pipe ends the remote command", command: "kubectl exec p -- cat f | kubectl --context k8s-dev apply -f -", wantCluster: "k8s-dev", wantStripped: "kubectl exec p -- cat f | kubectl apply -f -"},
		{name: "single-quoted value", command: "kubectl --context 'k8s dev' get ns", wantCluster: "k8s dev", wantStripped: "kubectl get ns"},
		{name: "not a word boundary", command: "kubectl get pods --contexts x", wantStripped: "kubectl get pods --contexts x"},
		{name: "flag with no value", command: "kubectl get pods --context", wantStripped: "kubectl get pods --context"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RequestedKubernetesCluster(tc.command, nil)
			require.NoError(t, err)
			require.Equal(t, tc.wantCluster, got)
			require.Equal(t, tc.wantStripped, StripKubectlContextFlag(tc.command))
		})
	}
}

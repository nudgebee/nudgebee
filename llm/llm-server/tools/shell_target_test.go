package tools

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"testing"
)

func cacheShellTargets(t *testing.T, account, owner string, configs []core.ToolConfig) {
	t.Helper()
	data, err := common.MarshalJson(configs)
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(core.CacheNamespaceLlmToolConfig, fmt.Sprintf("list_tool_configs:%s:%s", account, owner), data))
}
func TestShellTargetSelection(t *testing.T) {
	const account = "shell-target-selection"
	configs := []core.ToolConfig{
		{Name: "allowed", Values: []core.ToolConfigValue{{Name: "id", Value: "target-a"}}},
		{Name: "other", Values: []core.ToolConfigValue{{Name: "id", Value: "target-b"}}},
	}
	cacheShellTargets(t, account, ToolExecuteAwsCliCommand, configs)
	tool := ShellTool{AccountId: account}
	ctx := core.NewNbToolContext(security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "target-a"}), tool, account, "user", "conversation", "message", "agent", "", nil, "", core.NBQueryConfig{}, "call")
	selected, err := ResolveShellTarget(ctx, ToolExecuteAwsCliCommand)
	require.NoError(t, err)
	require.Equal(t, "allowed", selected.Name)
	ctx.QueryConfig.ToolConfigs = map[string]string{ToolExecuteAwsCliCommand: "other"}
	_, err = ResolveShellTarget(ctx, ToolExecuteAwsCliCommand)
	require.ErrorContains(t, err, "unavailable or unauthorized")
	ctx.Ctx = security.NewRequestContextForSuperAdmin()
	ctx.QueryConfig.ToolConfigs = nil
	_, err = ResolveShellTarget(ctx, ToolExecuteAwsCliCommand)
	require.ErrorContains(t, err, "selection")
	ctx.QueryConfig.ToolConfigs = map[string]string{ToolExecuteAwsCliCommand: "allowed"}
	selected, err = ResolveShellTarget(ctx, ToolExecuteAwsCliCommand)
	require.NoError(t, err)
	require.Equal(t, "allowed", selected.Name)
}

func TestRequestedShellAwsProfileForApprovalScope(t *testing.T) {
	owner, requested, err := RequestedShellTargetFromInput(`{"command":"aws --profile prod s3 ls"}`)
	require.NoError(t, err)
	require.Equal(t, ToolExecuteAwsCliCommand, owner)
	require.Equal(t, "prod", requested)

	owner, requested, err = RequestedShellTargetFromInput(`{"command":"aws --profile prod --profile dev s3 ls"}`)
	require.NoError(t, err)
	require.Equal(t, ToolExecuteAwsCliCommand, owner)
	require.Equal(t, "dev", requested, "one invocation uses its last --profile, as the aws CLI does")

	// Several invocations naming different profiles cannot be approved as one
	// target: approving the last would let an earlier write run on another
	// account (#39373 review).
	for _, command := range []string{
		"aws --profile prod s3 rm s3://bucket/key; aws --profile dev s3 ls",
		"aws --profile prod s3 rm s3://bucket/key && aws --profile dev s3 ls",
	} {
		owner, requested, err = RequestedShellTargetFromInput(fmt.Sprintf(`{"command":%q}`, command))
		require.ErrorIs(t, err, ErrAmbiguousShellTarget, command)
		require.Equal(t, ToolExecuteAwsCliCommand, owner)
		require.Empty(t, requested)
		_, err = RequestedShellTarget(ToolExecuteAwsCliCommand, command)
		require.ErrorIs(t, err, ErrAmbiguousShellTarget, command)
		require.Equal(t, ShellTool{}.Name()+"@"+fmt.Sprintf(`{"command":%q}`, command), ShellTool{}.ConfirmationKey(fmt.Sprintf(`{"command":%q}`, command)), "approval must be scoped to the exact input")
	}
	_, _, err = RequestedShellTargetFromInput(`{"command":"aws --profile dev s3 ls; aws --profile dev s3 rm s3://bucket/key"}`)
	require.NoError(t, err, "the same profile across invocations is one target")
	_, _, err = RequestedShellTargetFromInput(`{"command":"echo --profile dev && aws s3 rm s3://bucket/key"}`)
	require.ErrorContains(t, err, "aws-led command")
	for _, command := range []string{
		"env AWS_REGION=us-east-1 aws --profile prod s3 ls",
		"env --unset PATH aws --profile prod s3 ls",
		"sudo -u admin aws --profile prod s3 ls",
	} {
		_, _, err = RequestedShellTargetFromInput(fmt.Sprintf(`{"command":%q}`, command))
		require.ErrorContains(t, err, "aws-led command", command)
	}
}

// Regression for #38588 review: a region loop is a read the shim routes per
// invocation. Only approval/write paths may refuse it as ambiguous.
const awsRegionLoop = `for r in us-east-1 us-west-2; do aws elbv2 describe-load-balancers --region "$r" --profile aws-prod; done`

func TestShellAwsScriptIsAmbiguousOnlyForApproval(t *testing.T) {
	owner, requested, err := RequestedShellTargetFromCall(core.NBToolCallRequest{Command: awsRegionLoop})
	require.ErrorIs(t, err, ErrAmbiguousShellTarget)
	require.Equal(t, ToolExecuteAwsCliCommand, owner)
	require.Empty(t, requested)
	require.Error(t, ShellTool{}.ValidateWriteTargetInput(fmt.Sprintf(`{"command":%q}`, awsRegionLoop)))
}

func TestShellAwsScriptExecutesWithoutDelegatedTarget(t *testing.T) {
	const account = "origin-aws-loop"
	capture := &targetWorkspaceCapture{}
	tool := ShellTool{AccountId: account, workspaceManager: capture}
	ctx := core.NewNbToolContext(security.NewRequestContextForSuperAdmin(), tool, account, "user", "conversation", "message", "agent", "", nil, "", core.NBQueryConfig{ToolConfigs: map[string]string{ToolExecuteAwsCliCommand: "aws-dev"}}, "call")
	_, err := tool.Call(ctx, core.NBToolCallRequest{Command: awsRegionLoop})
	require.NoError(t, err)
	require.Equal(t, awsRegionLoop, capture.command)
	require.Equal(t, "aws-dev", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
}

func TestDelegatedShellAwsScriptKeepsChildDefault(t *testing.T) {
	prepared, err := ShellTool{}.PrepareDelegatedTargetInput(&core.ExecutionTarget{Provider: "aws", Name: "bound"}, fmt.Sprintf(`{"command":%q}`, awsRegionLoop))
	require.NoError(t, err)
	require.Equal(t, "bound", RequestedCloudAccount(core.ParseNBToolCallRequestFromInput(prepared).Arguments))
}

// Regression guard: without a delegated target, common shell shapes run as
// before this feature — no error, argv untouched, only the saved default.
func TestUntargetedShellShapesUnchanged(t *testing.T) {
	const account = "untargeted-shell-shapes"
	saved := map[string]string{ToolExecuteAwsCliCommand: "aws-dev", ToolExecuteKubectlCommand: "k8s-dev"}
	cacheShellTargets(t, account, ToolExecuteKubectlCommand, []core.ToolConfig{{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: account}}}})
	for _, tc := range []struct{ command, wantDefault string }{
		{"aws s3 ls", "aws-dev"},
		{"aws s3 ls | head -5", "aws-dev"},
		{"cd /tmp && aws s3 ls", "aws-dev"},
		{awsRegionLoop, "aws-dev"},
		{"AWS_REGION=us-east-1 aws ec2 describe-instances", "aws-dev"},
		{"kubectl get pods -A | grep checkout", "k8s-dev"},
		{"kubectl get ns && kubectl get pods -n default", "k8s-dev"},
		{"python3 process.py", ""},
		{"ls -la && cat notes.txt", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			capture := &targetWorkspaceCapture{}
			tool := ShellTool{AccountId: account, workspaceManager: capture}
			ctx := core.NewNbToolContext(security.NewRequestContextForSuperAdmin(), tool, account, "user", "conversation", "message", "agent", "", nil, "", core.NBQueryConfig{ToolConfigs: saved}, "call")
			_, err := tool.Call(ctx, core.NBToolCallRequest{Command: tc.command})
			require.NoError(t, err)
			require.Equal(t, tc.command, capture.command, "argv must not be rewritten")
			require.Equal(t, tc.wantDefault, capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
			if target := capture.env[workspace.ENV_NB_TARGET_ACCOUNT_ID]; target != "" {
				require.Equal(t, account, target, "only the conversation's own saved target may be pinned")
			}
		})
	}
}

func TestShellAwsAccountMustAgreeWithProfile(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		ambiguous         bool
	}{
		{"account only", `{"account":"aws-prod","command":"aws s3 rm s3://b/k"}`, "aws-prod", false},
		{"matching profile", `{"account":"aws-prod","command":"aws --profile AWS-PROD s3 rm s3://b/k"}`, "aws-prod", false},
		{"conflicting profile", `{"account":"aws-prod","command":"aws --profile aws-dev s3 rm s3://b/k"}`, "", true},
		{"conflicting profile in script", `{"account":"aws-prod","command":"for r in a b; do aws --profile aws-dev s3 ls; done"}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, requested, err := RequestedShellTargetFromInput(tc.input)
			require.Equal(t, ToolExecuteAwsCliCommand, owner)
			require.Equal(t, tc.want, requested)
			if !tc.ambiguous {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrAmbiguousShellTarget)
			require.Error(t, ShellTool{}.ValidateWriteTargetInput(tc.input))
			require.Equal(t, ShellTool{}.Name()+"@"+tc.input, ShellTool{}.ConfirmationKey(tc.input), "approval must be scoped to the exact input")
		})
	}
}

type targetWorkspaceCapture struct {
	workspace.WorkspaceManager
	account, conversation, command string
	env                            map[string]string
}

func (m *targetWorkspaceCapture) ExecuteOrLazyCreate(_ *security.RequestContext, account, conversation, command string, env map[string]string) (string, error) {
	m.account, m.conversation, m.command, m.env = account, conversation, command, env
	return "pod-a", nil
}
func TestShellKubernetesTargetKeepsConversationWorkspace(t *testing.T) {
	const account = "original-aws-workspace"
	cacheShellTargets(t, account, ToolExecuteKubectlCommand, []core.ToolConfig{{Name: "target-cluster", Values: []core.ToolConfigValue{{Name: "id", Value: "different-k8s-account"}}}})
	capture := &targetWorkspaceCapture{}
	tool := ShellTool{AccountId: account, workspaceManager: capture}
	ctx := core.NewNbToolContext(security.NewRequestContextForSuperAdmin(), tool, account, "user", "conversation", "message", "agent", "", nil, "", core.NBQueryConfig{ToolConfigs: map[string]string{ToolExecuteKubectlCommand: "target-cluster"}}, "call")
	_, err := tool.Call(ctx, core.NBToolCallRequest{Command: "kubectl get pods > pods.txt"})
	require.NoError(t, err)
	require.Equal(t, account, capture.account)
	require.Equal(t, "conversation", capture.conversation)
	require.Equal(t, "target-cluster", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
	require.Equal(t, "kubectl get pods > pods.txt", capture.command)
}

func TestDelegatedShellTargetIsPerCommandEnvironment(t *testing.T) {
	const account = "origin-workspace"
	capture := &targetWorkspaceCapture{}
	tool := ShellTool{AccountId: account, workspaceManager: capture}
	queryConfig := core.NBQueryConfig{
		ToolConfigs:     map[string]string{ToolExecuteAwsCliCommand: "origin-aws"},
		DelegatedTarget: &core.ExecutionTarget{Provider: "aws", Name: "prod-aws"},
	}
	ctx := core.NewNbToolContext(security.NewRequestContextForSuperAdmin(), tool, account, "user", "conversation", "message", "agent", "", nil, "", queryConfig, "call")
	_, err := tool.Call(ctx, core.NBToolCallRequest{Command: "python3 process.py"})
	require.NoError(t, err)
	require.Equal(t, "prod-aws", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
	require.Equal(t, "origin-aws", queryConfig.ToolConfigs[ToolExecuteAwsCliCommand])
}

func TestDelegatedShellCloudDefaultVerifiesConnectionIdentity(t *testing.T) {
	for _, tc := range []struct{ provider, owner, command string }{
		{"aws", ToolExecuteAwsCliCommand, "aws s3 ls"},
		{"gcp", ToolExecuteGcpCliCommand, "gcloud projects list"},
		{"azure", ToolExecuteAzureCliCommand, "az account list"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			account := "replaced-shell-" + tc.provider
			cacheShellTargets(t, account, tc.owner, []core.ToolConfig{{Name: "prod", Values: []core.ToolConfigValue{{Name: "id", Value: "replacement-id"}}}})
			capture := &targetWorkspaceCapture{}
			tool := ShellTool{AccountId: account, workspaceManager: capture}
			ctx := core.NewNbToolContext(security.NewRequestContextForSuperAdmin(), tool, account, "user", "conversation", "message", "agent", "", nil, "", core.NBQueryConfig{
				DelegatedTarget: &core.ExecutionTarget{Provider: tc.provider, Name: "prod", AccountID: "original-id"},
			}, "call")
			_, err := tool.Call(ctx, core.NBToolCallRequest{Command: tc.command})
			require.ErrorContains(t, err, "changed since handoff")
			require.Empty(t, capture.command, "workspace must not execute against a replaced connection")
		})
	}
}

func TestDelegatedShellAwsProfileOverridesDefault(t *testing.T) {
	tool := ShellTool{}
	prepared, err := tool.PrepareDelegatedTargetInput(&core.ExecutionTarget{Provider: "aws", Name: "bound", AccountID: "bound-id"}, `{"command":"aws --profile other s3 ls"}`)
	require.NoError(t, err)
	request := core.ParseNBToolCallRequestFromInput(prepared)
	owner, requested, err := RequestedShellTargetFromCall(request)
	require.NoError(t, err)
	require.Equal(t, ToolExecuteAwsCliCommand, owner)
	require.Equal(t, "other", requested)
	require.Empty(t, RequestedCloudAccount(request.Arguments), "bound default must not be added over an explicit profile")
}

func TestDelegatedShellTargetDoesNotOverrideOtherProvider(t *testing.T) {
	const account = "origin-workspace"
	for _, tc := range []struct {
		name, command, provider, target, selected string
	}{
		{"kubernetes target with AWS command", "aws s3 ls", "kubernetes", "cluster-prod", "aws-prod"},
		{"kubernetes target with AWS command and no AWS default", "aws s3 ls", "kubernetes", "cluster-prod", ""},
		{"AWS target with GCP command", "gcloud projects list", "aws", "aws-prod", "gcp-prod"},
		{"AWS target with Azure command", "az account list", "aws", "aws-prod", "azure-prod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := ShellConfigToolName(tc.command)
			capture := &targetWorkspaceCapture{}
			tool := ShellTool{AccountId: account, workspaceManager: capture}
			queryConfig := core.NBQueryConfig{
				ToolConfigs:     map[string]string{owner: tc.selected},
				DelegatedTarget: &core.ExecutionTarget{Provider: tc.provider, Name: tc.target},
			}
			ctx := core.NewNbToolContext(security.NewRequestContextForSuperAdmin(), tool, account, "user", "conversation", "message", "agent", "", nil, "", queryConfig, "call")
			_, err := tool.Call(ctx, core.NBToolCallRequest{Command: tc.command})
			require.NoError(t, err)
			require.Equal(t, tc.selected, capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
		})
	}
}

// Found in #38588 testing: a loop over clusters was refused with
// `cluster "$c" is not connected`. A variable selects no cluster in the
// script text; reads run per invocation, writes are refused before approval.
func TestRequestedShellTarget_VariableContext(t *testing.T) {
	for _, command := range []string{
		`for c in k8s-dev k8s-prod; do kubectl --context $c get ns; done`,
		`for c in k8s-dev k8s-prod; do kubectl --context "$c" get ns; done`,
		"kubectl --context=${CLUSTER} get pods",
		"kubectl --context `cat ctx` get pods",
	} {
		input := fmt.Sprintf(`{"command":%q}`, command)
		owner, requested, err := RequestedShellTargetFromInput(input)
		require.ErrorIs(t, err, ErrAmbiguousShellTarget, command)
		require.Equal(t, ToolExecuteKubectlCommand, owner, command)
		require.Empty(t, requested, command)
		require.ErrorContains(t, ShellTool{}.ValidateWriteTargetInput(input), "shell variable", command)
	}
	_, err := RequestedHelmCluster("helm list --kube-context $c", nil)
	require.ErrorIs(t, err, ErrAmbiguousShellTarget)

	// The same for an aws --profile: a read runs per invocation, a write is
	// refused before approval.
	input := `{"command":"aws --profile $p s3 ls"}`
	owner, requested, err := RequestedShellTargetFromInput(input)
	require.ErrorIs(t, err, ErrAmbiguousShellTarget)
	require.Equal(t, ToolExecuteAwsCliCommand, owner)
	require.Empty(t, requested)
	require.ErrorContains(t, ShellTool{}.ValidateWriteTargetInput(`{"command":"aws --profile $p s3 rm s3://bucket/key"}`), "--profile is set through a shell variable")
}

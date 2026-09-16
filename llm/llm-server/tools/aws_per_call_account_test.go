package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"

	"github.com/stretchr/testify/require"
)

func awsCfg(name, id, number string) core.ToolConfig {
	return core.ToolConfig{Name: name, Values: []core.ToolConfigValue{{Name: "id", Value: id}, {Name: "account_number", Value: number}}}
}

// awsFixture mirrors production: the conversation account's own AWS list is
// narrowed to aws-prod, the tenant list holds every AWS connection, and two
// connections share one account number.
func awsFixture(t *testing.T, account string) {
	t.Helper()
	prod := awsCfg("aws-prod", "prod-id", "740395098545")
	cacheShellTargets(t, account, ToolExecuteAwsCliCommand, []core.ToolConfig{prod})
	StubTenantAccounts(t, "tenant", map[string][]core.ToolConfig{ToolExecuteAwsCliCommand: {
		prod,
		awsCfg("aws-demo", "demo-id", "136781626097"),
		awsCfg("dev-aws", "dev1-id", "864186153326"),
		awsCfg("Demo123", "dev2-id", "864186153326"),
		awsCfg("aws-secret", "no-access-id", "111111111111"),
	}})
	previous := awsAccountAuth
	awsAccountAuth = func(_ context.Context, accountId string) (*CloudAuthResult, error) {
		return &CloudAuthResult{Env: map[string]string{"AWS_ACCESS_KEY_ID": "key-" + accountId}}, nil
	}
	t.Cleanup(func() { awsAccountAuth = previous })
}

func awsCtx(t *testing.T, account string, tool core.NBTool) core.NbToolContext {
	t.Helper()
	sc := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "prod-id", "demo-id", "dev1-id", "dev2-id"})
	return core.NewNbToolContext(sc, tool, account, "user", "conversation", "message", "agent", "", nil, "",
		core.NBQueryConfig{ToolConfigs: map[string]string{ToolExecuteAwsCliCommand: "aws-prod"}}, "call")
}

func TestAwsExecutePerCallAccount(t *testing.T) {
	const account = "aws-per-call"
	awsFixture(t, account)

	run := func(t *testing.T, req core.NBToolCallRequest) (*targetWorkspaceCapture, core.NBToolResponse, error) {
		t.Helper()
		capture := &targetWorkspaceCapture{}
		tool := AwsCliTool{workspaceManager: capture}
		resp, err := tool.Call(awsCtx(t, account, tool), req)
		return capture, resp, err
	}

	t.Run("no override stays on the saved account", func(t *testing.T) {
		capture, resp, err := run(t, core.NBToolCallRequest{Command: "aws sts get-caller-identity"})
		require.NoError(t, err)
		require.Equal(t, "key-prod-id", capture.env["AWS_ACCESS_KEY_ID"])
		require.Equal(t, "aws-prod", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
		require.False(t, strings.HasPrefix(resp.Data, "[account:"))
	})

	t.Run("account argument uses that account's credentials for this call", func(t *testing.T) {
		capture, resp, err := run(t, core.NBToolCallRequest{Command: "aws sts get-caller-identity", Arguments: map[string]any{"account": "aws-demo"}})
		require.NoError(t, err)
		require.Equal(t, "key-demo-id", capture.env["AWS_ACCESS_KEY_ID"])
		require.Equal(t, "aws-demo", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME])
		require.Equal(t, account, capture.account, "the conversation workspace does not move")
		require.True(t, strings.HasPrefix(resp.Data, "[account: aws-demo]\n"), resp.Data)
	})

	t.Run("profile flag does not switch accounts", func(t *testing.T) {
		capture, _, err := run(t, core.NBToolCallRequest{Command: "aws --profile aws-demo s3 ls"})
		require.NoError(t, err)
		require.Equal(t, "key-prod-id", capture.env["AWS_ACCESS_KEY_ID"])
		require.Equal(t, "aws --profile aws-demo s3 ls", capture.command, "commands are not parsed for AWS")
	})

	t.Run("a unique account id selects that account", func(t *testing.T) {
		capture, _, err := run(t, core.NBToolCallRequest{Command: "aws s3 ls", Arguments: map[string]any{"account": "136781626097"}})
		require.NoError(t, err)
		require.Equal(t, "key-demo-id", capture.env["AWS_ACCESS_KEY_ID"])
	})

	t.Run("the saved account's own id stays on the saved account", func(t *testing.T) {
		capture, resp, err := run(t, core.NBToolCallRequest{Command: "aws s3 ls", Arguments: map[string]any{"account": "740395098545"}})
		require.NoError(t, err)
		require.Equal(t, "key-prod-id", capture.env["AWS_ACCESS_KEY_ID"])
		require.False(t, strings.HasPrefix(resp.Data, "[account:"))
	})

	t.Run("a shared account id is refused with the matching names", func(t *testing.T) {
		capture, resp, err := run(t, core.NBToolCallRequest{Command: "aws s3 ls", Arguments: map[string]any{"account": "864186153326"}})
		require.Error(t, err)
		require.Contains(t, resp.Data, "matches several connections (Demo123, dev-aws)")
		require.Empty(t, capture.command)
	})

	t.Run("an unreadable or unknown account runs nothing and lists usable names", func(t *testing.T) {
		for _, name := range []string{"aws-secret", "nope"} {
			capture, resp, err := run(t, core.NBToolCallRequest{Command: "aws s3 ls", Arguments: map[string]any{"account": name}})
			require.Error(t, err)
			require.Contains(t, resp.Data, "available accounts: aws-demo, aws-prod, Demo123, dev-aws")
			require.NotContains(t, resp.Data, "aws-secret,")
			require.Empty(t, capture.command)
		}
	})
}

func TestAwsExecuteNamesAccountOnCredentialFailure(t *testing.T) {
	const account = "aws-cred-failure"
	awsFixture(t, account)
	awsAccountAuth = func(_ context.Context, accountId string) (*CloudAuthResult, error) {
		return nil, fmt.Errorf("PERMANENT ERROR: AWS STS AssumeRole failed for %s", accountId)
	}
	capture := &targetWorkspaceCapture{}
	tool := AwsCliTool{workspaceManager: capture}
	resp, err := tool.Call(awsCtx(t, account, tool), core.NBToolCallRequest{Command: "aws sts get-caller-identity", Arguments: map[string]any{"account": "aws-demo"}})
	require.NoError(t, err, "permanent errors are returned as data, as before")
	require.True(t, strings.HasPrefix(resp.Data, "[account: aws-demo]\nPERMANENT ERROR"), resp.Data)
	require.Empty(t, capture.command)
}

func TestShellAwsAlwaysUsesSavedAccount(t *testing.T) {
	const account = "shell-aws-saved"
	awsFixture(t, account)

	capture := &targetWorkspaceCapture{}
	tool := ShellTool{AccountId: account, workspaceManager: capture}
	_, err := tool.Call(awsCtx(t, account, tool), core.NBToolCallRequest{Command: "aws --profile aws-demo sts get-caller-identity"})
	require.NoError(t, err)
	require.Equal(t, "key-prod-id", capture.env["AWS_ACCESS_KEY_ID"])
	require.Equal(t, "", ShellTool{}.ConfirmationKey(`{"command":"aws --profile aws-demo s3 rm s3://b/k"}`))
}

func TestAwsConfirmationKeyIsPerAccount(t *testing.T) {
	aws := AwsCliTool{}
	require.Equal(t, "", aws.ConfirmationKey(`{"command":"aws ec2 terminate-instances --instance-ids i-1"}`))
	demo := aws.ConfirmationKey(`{"command":"aws ec2 terminate-instances --instance-ids i-1","account":"aws-demo"}`)
	require.Equal(t, "aws_execute@aws-demo", demo)
	require.NotEqual(t, demo, aws.ConfirmationKey(`{"command":"aws ec2 terminate-instances --instance-ids i-1","account":"aws-prod"}`))
	require.Equal(t, demo, aws.ConfirmationKey(`{"command":"aws ec2 terminate-instances --instance-ids i-1","account":"AWS-DEMO"}`))
	require.Equal(t, "", aws.ConfirmationKey(`aws --profile aws-demo ec2 terminate-instances --instance-ids i-1`))
}

func TestAwsIdentifyConfigPrefersExplicitAccount(t *testing.T) {
	configs := []core.ToolConfig{
		awsCfg("aws-prod", "prod-id", "740395098545"),
		awsCfg("aws-demo", "demo-id", "136781626097"),
		awsCfg("dev-aws", "dev1-id", "864186153326"),
		awsCfg("Demo123", "dev2-id", "864186153326"),
	}
	ctx := core.NbToolContext{QueryConfig: core.NBQueryConfig{Labels: map[string]any{"nb_cloud_account_id": "740395098545"}}}
	aws := AwsCliTool{}

	got, err := aws.IdentifyConfig(ctx, core.NBToolCallRequest{Command: `{"command":"aws s3 ls","account":"aws-demo"}`}, configs)
	require.NoError(t, err)
	require.Equal(t, "aws-demo", got.Name, "explicit account beats the UI page label")

	got, err = aws.IdentifyConfig(ctx, core.NBToolCallRequest{Command: `{"command":"aws s3 ls","account":"136781626097"}`}, configs)
	require.NoError(t, err)
	require.Equal(t, "aws-demo", got.Name, "a unique account id selects that account")

	got, err = aws.IdentifyConfig(ctx, core.NBToolCallRequest{Command: `{"command":"aws s3 ls","account":"864186153326"}`}, configs)
	require.NoError(t, err)
	require.Equal(t, "aws-prod", got.Name, "an ambiguous id falls through to the page label")
}

func TestRenderExecutionTargets(t *testing.T) {
	const account = "render-targets"
	awsFixture(t, account)
	StubTenantAccounts(t, "tenant", map[string][]core.ToolConfig{
		ToolExecuteAwsCliCommand: {awsCfg("aws-prod", "prod-id", "740395098545"), awsCfg("aws-demo", "demo-id", "136781626097"), awsCfg("aws-secret", "no-access-id", "1")},
		ToolExecuteKubectlCommand: {
			{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "prod-id"}}},
			{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: "demo-id"}}},
		},
	})
	sc := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "prod-id", "demo-id"})
	saved := map[string]string{ToolExecuteAwsCliCommand: "aws-prod", ToolExecuteKubectlCommand: "k8s-prod"}

	block := RenderExecutionTargets(sc, account, saved, []string{ToolExecuteAwsCliCommand, core.ToolExecuteShellCommand})
	require.Contains(t, block, "<execution_targets>")
	require.Contains(t, block, "Kubernetes clusters: current k8s-prod; others you can use: k8s-dev.")
	require.Contains(t, block, "AWS accounts: current aws-prod; others you can use: aws-demo (136781626097).")
	require.NotContains(t, block, "aws-secret", "accounts the user cannot read are not listed")

	require.NotContains(t, RenderExecutionTargets(sc, account, saved, []string{core.ToolExecuteShellCommand}), "AWS accounts",
		"shell aws cannot switch accounts, so AWS is listed only for aws_execute")
	require.Empty(t, RenderExecutionTargets(sc, account, saved, []string{"logs"}), "no targetable tool, no block")
}

func TestKubectlConfigIsRefusedWithClusterList(t *testing.T) {
	require.True(t, kubectlUsesConfigSubcommand("kubectl config get-contexts"))
	require.True(t, kubectlUsesConfigSubcommand("kubectl --request-timeout 5s config view | head"))
	require.True(t, kubectlUsesConfigSubcommand("echo x && /usr/local/bin/kubectl config current-context"))
	require.False(t, kubectlUsesConfigSubcommand("kubectl get configmaps -A"))
	require.False(t, kubectlUsesConfigSubcommand(`kubectl exec pod -- sh -c "kubectl config view"`))
	require.False(t, kubectlUsesConfigSubcommand("kubectl get cm config -n x"))

	const account = "kubectl-config-refused"
	clusterFixture(t, account)
	for _, tool := range []core.NBTool{KubectlExecuteTool{workspaceManager: &targetWorkspaceCapture{}}, ShellTool{AccountId: account, workspaceManager: &targetWorkspaceCapture{}}} {
		resp, err := tool.Call(kubectlCallCtx(t, account, tool), core.NBToolCallRequest{Command: "kubectl config get-contexts"})
		require.Error(t, err, tool.Name())
		require.Contains(t, resp.Data, "Current cluster: k8s-prod. Other clusters you can use: k8s-dev, k8s-dev-oss.")
		require.Contains(t, resp.Data, `cluster="<name>"`)
	}
}

package api

import (
	"errors"
	"nudgebee/llm/cloud"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func awsTarget(name, id, number, region string) core.ToolConfig {
	return core.ToolConfig{Name: name, Values: []core.ToolConfigValue{
		{Name: "id", Value: id},
		{Name: "account_number", Value: number},
		{Name: "region", Value: region},
	}}
}

func stubAwsWorkspaceTargets(t *testing.T, configs []core.ToolConfig) {
	t.Helper()
	original := tools.TenantAccountLister
	tools.TenantAccountLister = func(_ *security.RequestContext, _ core.NBTool) ([]core.ToolConfig, error) {
		return configs, nil
	}
	t.Cleanup(func() { tools.TenantAccountLister = original })
}

func TestAwsWorkspaceHandlerExtractsProfileAndResolvesTarget(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"workspace", "prod-id"})
	stubAwsWorkspaceTargets(t, []core.ToolConfig{
		awsTarget("development", "dev-id", "111111111111", "us-east-1"),
		awsTarget("production", "prod-id", "222222222222", "eu-west-1"),
	})

	plan, err := (awsWorkspaceCommandHandler{}).Prepare(ctx, core.NbToolContext{}, WorkspaceExecuteRequest{
		AccountId: "workspace",
		Command:   "aws ec2 describe-instances --profile production --region eu-west-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "prod-id", plan.TargetAccountID)
	assert.Equal(t, "production", plan.Request.TargetName)
	assert.Equal(t, "aws 'ec2' 'describe-instances' '--region' 'eu-west-1'", plan.Request.Command)
}

func TestAwsWorkspaceHandlerRejectsConflictingProfiles(t *testing.T) {
	_, err := (awsWorkspaceCommandHandler{}).Prepare(nil, core.NbToolContext{}, WorkspaceExecuteRequest{
		Command: "aws --profile development ec2 describe-instances --profile=production",
	})
	require.ErrorContains(t, err, "more than one profile")
}

func TestVirtualAwsProfilesOnlyListsReadableTargets(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"workspace", "prod-id"})
	stubAwsWorkspaceTargets(t, []core.ToolConfig{
		awsTarget("development", "dev-id", "111111111111", "us-east-1"),
		awsTarget("production", "prod-id", "222222222222", "eu-west-1"),
	})

	plan, err := (awsWorkspaceCommandHandler{}).Prepare(ctx, core.NbToolContext{}, WorkspaceExecuteRequest{
		AccountId: "workspace",
		Command:   "aws configure list-profiles",
	})
	require.NoError(t, err)
	require.True(t, plan.Virtual)
	assert.Equal(t, "production\n", plan.VirtualResult)
}

func TestVirtualAwsRegionUsesSelectedProfile(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"workspace", "prod-id"})
	stubAwsWorkspaceTargets(t, []core.ToolConfig{awsTarget("production", "prod-id", "222222222222", "eu-west-1")})

	plan, err := (awsWorkspaceCommandHandler{}).Prepare(ctx, core.NbToolContext{}, WorkspaceExecuteRequest{
		AccountId: "workspace",
		Command:   "aws configure get region --profile production",
	})
	require.NoError(t, err)
	require.True(t, plan.Virtual)
	assert.Equal(t, "eu-west-1\n", plan.VirtualResult)
}

func TestAwsWorkspaceHandlerPreservesCloudProcessResult(t *testing.T) {
	original := executeCloudCliV2
	t.Cleanup(func() { executeCloudCliV2 = original })
	var captured cloud.CloudExecuteCliCommandRequest
	executeCloudCliV2 = func(req cloud.CloudExecuteCliCommandRequest) (cloud.CliExecutionResult, error) {
		captured = req
		return cloud.CliExecutionResult{Stdout: "partial", Stderr: "denied", ExitCode: 7}, nil
	}
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"aws-id"})
	plan := workspaceCommandPlan{
		Request:         WorkspaceExecuteRequest{Command: "aws s3 ls"},
		TargetAccountID: "aws-id",
	}
	result, err := (awsWorkspaceCommandHandler{}).Execute(ctx, core.NbToolContext{}, plan)
	var exitErr *tools.RelayCommandExitError
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, 7, exitErr.ExitCode)
	assert.Equal(t, "aws-id", captured.AccountID)
	assert.Equal(t, "tenant", captured.TenantID)
	assert.Equal(t, "user", captured.UserID)
	assert.Equal(t, map[string]any{
		"stdout": "partial", "stderr": "denied", "exit_code": 7, "timed_out": false, "output_truncated": false,
	}, result)
}

func TestCloudWorkspaceHandlersResolveTargetWithoutCredentials(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"workspace", "gcp-id", "azure-id"})
	tests := []struct {
		name       string
		handler    cloudWorkspaceCommandHandler
		configName string
		accountID  string
		command    string
		want       string
	}{
		{name: "gcloud", handler: cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteGcpCliCommand, executable: "gcloud"}, configName: "gcp-prod", accountID: "gcp-id", command: "gcloud compute instances list --format=json", want: "gcloud 'compute' 'instances' 'list' '--format=json'"},
		{name: "gsutil", handler: cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteGcpCliCommand, executable: "gsutil"}, configName: "gcp-prod", accountID: "gcp-id", command: "gsutil ls gs://bucket", want: "gsutil 'ls' 'gs://bucket'"},
		{name: "bq", handler: cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteGcpCliCommand, executable: "bq"}, configName: "gcp-prod", accountID: "gcp-id", command: "bq ls --project_id project", want: "bq 'ls' '--project_id' 'project'"},
		{name: "azure", handler: cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteAzureCliCommand, executable: "az"}, configName: "azure-prod", accountID: "azure-id", command: "az vm list --output json", want: "az 'vm' 'list' '--output' 'json'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubAwsWorkspaceTargets(t, []core.ToolConfig{awsTarget(tt.configName, tt.accountID, "", "")})
			plan, err := tt.handler.Prepare(ctx, core.NbToolContext{ToolConfig: awsTarget(tt.configName, tt.accountID, "", "")}, WorkspaceExecuteRequest{
				AccountId: "workspace", ConfigName: tt.configName, Command: tt.command,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.accountID, plan.TargetAccountID)
			assert.Equal(t, tt.want, plan.Request.Command)
		})
	}
}

func TestCloudWorkspaceHandlerRejectsExecutableMismatch(t *testing.T) {
	_, err := (cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteGcpCliCommand, executable: "gcloud"}).Prepare(nil, core.NbToolContext{}, WorkspaceExecuteRequest{Command: "az vm list"})
	require.ErrorContains(t, err, "must start with gcloud")
}

func TestCloudWorkspaceHandlerFallsBackToWorkspaceAccount(t *testing.T) {
	const workspaceAccount = "gcp-current-id"
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{workspaceAccount, "gcp-other-id"})
	stubAwsWorkspaceTargets(t, []core.ToolConfig{
		awsTarget("gcp-other", "gcp-other-id", "", ""),
		awsTarget("gcp-current", workspaceAccount, "", ""),
	})

	handler := cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteGcpCliCommand, executable: "gcloud"}
	plan, err := handler.Prepare(ctx, core.NbToolContext{}, WorkspaceExecuteRequest{
		AccountId: workspaceAccount,
		Command:   "gcloud projects list",
	})
	require.NoError(t, err)
	require.Equal(t, workspaceAccount, plan.TargetAccountID)
}

func TestKubernetesWorkspaceHandlerPreservesContextRouting(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"workspace", "dev-id"})
	stubTenantAccounts(t, "workspace", "dev-id")
	tools.StubTenantClusters(t, "tenant", []core.ToolConfig{
		{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "workspace"}}},
		{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: "dev-id"}}},
	})
	toolCtx := core.NbToolContext{
		Ctx:       ctx,
		AccountId: "workspace",
		ToolConfig: core.ToolConfig{
			Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "workspace"}},
		},
	}
	plan, err := (kubernetesWorkspaceCommandHandler{}).Prepare(ctx, toolCtx, WorkspaceExecuteRequest{
		AccountId: "workspace",
		Command:   "kubectl --context k8s-dev get pods",
	})
	require.NoError(t, err)
	assert.Equal(t, "dev-id", plan.TargetAccountID)
	assert.Equal(t, "k8s-dev", plan.Request.TargetName)
	assert.Equal(t, "kubectl 'get' 'pods'", plan.Request.Command)
}

func TestHelmWorkspaceTargetUsesSelectedCluster(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"workspace", "dev-id"})
	stubTenantAccounts(t, "workspace", "dev-id")
	tools.StubTenantClusters(t, "tenant", []core.ToolConfig{
		{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "workspace"}}},
		{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: "dev-id"}}},
	})
	toolCtx := core.NbToolContext{Ctx: ctx, AccountId: "workspace", ToolConfig: core.ToolConfig{Name: "k8s-prod"}}
	target, err := workspaceRelayTarget(toolCtx, tools.RelayJobHelm, "k8s-dev", "helm list -A")
	require.NoError(t, err)
	require.Equal(t, "dev-id", target)

	target, err = workspaceRelayTarget(toolCtx, tools.RelayJobHelm, "", "helm list -A")
	require.NoError(t, err)
	require.Equal(t, "workspace", target)

	_, err = workspaceRelayTarget(toolCtx, tools.RelayJobHelm, "unknown", "helm list -A")
	require.Error(t, err)
}

// TestAwsWorkspaceHandlerPrefersConfigNameOverOwnAccount reproduces #40647:
// the shim forwards the account aws_execute chose only as ConfigName, and the
// resolver used to return whichever of "own account" or "ConfigName" sorted
// first — so a conversation on Demo123 asking for dev-aws ran on Demo123 and
// the proxy refused it (the token is bound to dev-aws). Cases mirror the dev
// data: the result must not depend on name order.
func TestAwsWorkspaceHandlerPrefersConfigNameOverOwnAccount(t *testing.T) {
	targets := []core.ToolConfig{ // name-sorted, as ListAccountTargets returns them
		awsTarget("aws-demo", "aws-demo-id", "333333333333", "us-east-1"),
		awsTarget("aws-prod", "aws-prod-id", "444444444444", "us-east-1"),
		awsTarget("Demo123", "demo123-id", "555555555555", "us-east-1"),
		awsTarget("dev-aws", "dev-aws-id", "555555555555", "us-east-1"),
	}
	cases := []struct{ ownAccount, configName, want string }{
		{"demo123-id", "dev-aws", "dev-aws-id"},    // own sorts first: was demo123-id
		{"aws-demo-id", "dev-aws", "dev-aws-id"},   // own sorts first: was aws-demo-id
		{"aws-demo-id", "aws-prod", "aws-prod-id"}, // own sorts first: was aws-demo-id
		{"dev-aws-id", "Demo123", "demo123-id"},    // requested sorts first: always worked
		{"demo123-id", "", "demo123-id"},           // no ConfigName: own account
		{"demo123-id", "Demo123", "demo123-id"},    // ConfigName is the own account
		{"demo123-id", "no-such", "demo123-id"},    // unknown ConfigName: own account, as before
	}
	for _, c := range cases {
		t.Run(c.ownAccount+"->"+c.configName, func(t *testing.T) {
			stubAwsWorkspaceTargets(t, targets)
			ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"aws-demo-id", "aws-prod-id", "demo123-id", "dev-aws-id"})
			plan, err := (awsWorkspaceCommandHandler{}).Prepare(ctx, core.NbToolContext{}, WorkspaceExecuteRequest{
				AccountId:  c.ownAccount,
				ConfigName: c.configName,
				Command:    "aws lambda get-function --function-name x --region us-east-1",
			})
			require.NoError(t, err)
			assert.Equal(t, c.want, plan.TargetAccountID)
		})
	}
}

// An explicit --profile still decides the account; ConfigName does not override it.
func TestAwsWorkspaceHandlerProfileWinsOverConfigName(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"demo123-id", "dev-aws-id"})
	stubAwsWorkspaceTargets(t, []core.ToolConfig{
		awsTarget("Demo123", "demo123-id", "555555555555", "us-east-1"),
		awsTarget("dev-aws", "dev-aws-id", "555555555555", "us-east-1"),
	})

	plan, err := (awsWorkspaceCommandHandler{}).Prepare(ctx, core.NbToolContext{}, WorkspaceExecuteRequest{
		AccountId:  "demo123-id",
		ConfigName: "dev-aws",
		Command:    "aws s3 ls --profile Demo123",
	})
	require.NoError(t, err)
	assert.Equal(t, "demo123-id", plan.TargetAccountID)
}

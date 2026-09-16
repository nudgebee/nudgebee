package core

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/require"
)

// A shell kubectl write is checked against the cluster --context names, not the
// conversation's saved cluster, so write access on the saved cluster cannot
// authorize a write that actually lands elsewhere.
func TestShellWriteTargetUsesRequestedCluster(t *testing.T) {
	const account = "shell-write-target"
	configs := []toolcore.ToolConfig{
		{Name: "k8s-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "prod-id"}}},
		{Name: "k8s-dev", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "dev-id"}}},
	}
	data, err := common.MarshalJson(configs)
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, fmt.Sprintf("list_tool_configs:%s:%s", account, tools.ToolExecuteKubectlCommand), data))
	tools.StubTenantClusters(t, "tenant", configs)

	owner, ok := toolcore.GetNBTool(account, tools.ToolExecuteKubectlCommand)
	require.True(t, ok)
	e := &plannerExecutor{
		ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "prod-id", "dev-id"}),
		agentRequest: NBAgentRequest{AccountId: account, QueryConfig: toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{tools.ToolExecuteKubectlCommand: "k8s-prod"},
		}},
	}

	got, err := e.shellWriteTarget(owner, NBAgentPlannerToolAction{ToolInput: `{"command":"kubectl --context k8s-dev delete pod x -n app"}`})
	require.NoError(t, err)
	require.Equal(t, "k8s-dev", got.Name)

	got, err = e.shellWriteTarget(owner, NBAgentPlannerToolAction{ToolInput: `{"command":"kubectl delete pod x -n app"}`})
	require.NoError(t, err)
	require.Equal(t, "k8s-prod", got.Name)

	_, err = e.shellWriteTarget(owner, NBAgentPlannerToolAction{ToolInput: `{"command":"kubectl --context nope delete pod x"}`})
	require.ErrorContains(t, err, "available clusters")
}

// A write that names another AWS account is checked against that account.
func TestPerCallWriteTargetUsesNamedAccount(t *testing.T) {
	const account = "aws-write-target"
	prod := toolcore.ToolConfig{Name: "aws-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "prod-id"}, {Name: "account_number", Value: "1"}}}
	demo := toolcore.ToolConfig{Name: "aws-demo", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "demo-id"}, {Name: "account_number", Value: "2"}}}
	data, err := common.MarshalJson([]toolcore.ToolConfig{prod})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, fmt.Sprintf("list_tool_configs:%s:%s", account, tools.ToolExecuteAwsCliCommand), data))
	tools.StubTenantAccounts(t, "tenant", map[string][]toolcore.ToolConfig{tools.ToolExecuteAwsCliCommand: {prod, demo}})

	tool, ok := toolcore.GetNBTool(account, tools.ToolExecuteAwsCliCommand)
	require.True(t, ok)
	scoped, ok := tool.(toolcore.PerCallTargetTool)
	require.True(t, ok)
	e := &plannerExecutor{
		ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "prod-id", "demo-id"}),
		agentRequest: NBAgentRequest{AccountId: account, QueryConfig: toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{tools.ToolExecuteAwsCliCommand: "aws-prod"},
		}},
	}

	got, found, err := e.perCallWriteTarget(tool, scoped, NBAgentPlannerToolAction{ToolInput: `{"command":"aws ec2 terminate-instances --instance-ids i-1","account":"aws-demo"}`})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "aws-demo", got.Name)

	_, found, err = e.perCallWriteTarget(tool, scoped, NBAgentPlannerToolAction{ToolInput: `{"command":"aws ec2 terminate-instances --instance-ids i-1"}`})
	require.NoError(t, err)
	require.False(t, found, "no named account: the existing authorization applies")

	e.ctx = security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "prod-id"})
	_, _, err = e.perCallWriteTarget(tool, scoped, NBAgentPlannerToolAction{ToolInput: `{"command":"aws s3 rm s3://b/k","account":"aws-demo"}`})
	require.ErrorContains(t, err, "not connected or you do not have access")
}

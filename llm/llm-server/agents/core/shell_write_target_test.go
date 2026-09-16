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

package agents

import (
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// Unmentioned cost/spend questions default-route to the account's debug
// orchestrator (the router reaches finops only via an @mention on a fresh
// conversation), so every orchestrator a cost question can land on — the K8s
// lean core and each cloud lean orchestrator — must preload finops as a
// callable sub-agent. Otherwise cost questions get answered with hand-written
// billing CLI calls, kubectl/metrics guesswork, or from the recommendations
// list alone.
func TestDebugAgents_PreloadFinops(t *testing.T) {
	const testAccountId = "test-finops-subagent-inclusion"
	sc := security.NewRequestContextForSuperAdmin()

	resolvedNames := func(nbTools []toolcore.NBTool) []string {
		names := make([]string, 0, len(nbTools))
		for _, tool := range nbTools {
			names = append(names, tool.Name())
		}
		return names
	}

	t.Run("k8s lean core names include finops", func(t *testing.T) {
		assert.True(t, containsTool(trimmedK8sCoreToolNames(), FinOpsAgentName),
			"k8s_lean must preload finops — cost questions default-route here and nothing steers on-demand search to it")
	})

	t.Run("k8s lean resolved set includes finops", func(t *testing.T) {
		names := resolvedNames(getTrimmedK8sSupportedTools(sc, testAccountId, "k8s_lean_finops_inclusion"))
		assert.Contains(t, names, FinOpsAgentName)
	})

	t.Run("cloud lean core names include finops", func(t *testing.T) {
		assert.True(t, containsTool(cloudLeanCoreToolNames(tools.ToolExecuteAwsCliCommand), FinOpsAgentName),
			"cloud lean orchestrators must preload finops — cost questions on cloud accounts default-route here")
	})

	cloudOrchestrators := []struct {
		name        string
		cliToolName string
	}{
		{AgentAwsOrchestratorName, tools.ToolExecuteAwsCliCommand},
		{AgentGcpOrchestratorName, tools.ToolExecuteGcpCliCommand},
		{AgentAzureOrchestratorName, tools.ToolExecuteAzureCliCommand},
	}
	for _, tc := range cloudOrchestrators {
		t.Run(tc.name+" resolved set includes finops", func(t *testing.T) {
			names := resolvedNames(getCloudLeanSupportedTools(sc, testAccountId, tc.name+"_finops_inclusion", tc.cliToolName))
			assert.Contains(t, names, FinOpsAgentName)
		})
	}
}

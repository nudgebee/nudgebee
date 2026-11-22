package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"
)

// metrics and traces hand a Datadog account's question to datadog_metrics and
// datadog_traces with the target attached; an agent that refused it would
// fail every Datadog read on another account.
func TestDatadogAgentsFollowTheTarget(t *testing.T) {
	leaves := []core.NBAgent{
		DatadogContainersAgent{accountId: "origin-id"}, DatadogContainersQueryAgent{}, DatadogEventsAgent{accountId: "origin-id"},
		DatadogHostsAgent{accountId: "origin-id"}, DatadogIncidentAgent{accountId: "origin-id"},
		DatadogMetricsAgent{accountId: "origin-id"}, DatadogMetricsQueryAgent{},
		DatadogServiceAgent{accountId: "origin-id"}, DatadogSoftwareCatalogAgent{accountId: "origin-id"}, DatadogTracesAgent{accountId: "origin-id"},
	}
	for _, agent := range leaves {
		_, aware := agent.(core.ExecutionTargetAwareAgent)
		require.True(t, aware, agent.GetName())
		for _, tool := range agent.GetSupportedTools(nil) {
			assert.True(t, toolcore.SupportsDelegatedTarget(tool) || toolcore.IsTargetNeutral(tool.Name()), "%s: %s cannot take the target", agent.GetName(), tool.Name())
		}
	}
	_, aware := core.NBAgent(&DatadogOrchestratorAgent{}).(core.ExecutionTargetAwareAgent)
	require.True(t, aware, AgentDatadogOrchestratorName)

	for _, agent := range append(leaves, &DatadogOrchestratorAgent{}) {
		tool, ok := toolcore.GetNBTool("origin-id", agent.GetName())
		require.True(t, ok, agent.GetName())
		assert.True(t, toolcore.SupportsDelegatedTarget(tool), "the %s agent-tool must accept a target", agent.GetName())
	}
}

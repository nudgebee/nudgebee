package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"
)

func assertAgentHasTool(t *testing.T, agent interface {
	GetSupportedTools(*security.RequestContext) []toolcore.NBTool
}, name string) {
	t.Helper()
	for _, tool := range agent.GetSupportedTools(nil) {
		if tool.Name() == name {
			return
		}
	}
	assert.Fail(t, "agent does not expose tool", "tool=%s", name)
}

func TestOpenObserveAgentsExposeProviderTools(t *testing.T) {
	provider := services_server.ObservabilityProvider{Provider: " OpenObserve "}
	logAgent := newLogAgent("acct", provider)
	assertAgentHasTool(t, logAgent, "openobserve_logs_execute")
	assert.Contains(t, logAgent.GetSystemPrompt(nil, core.NBAgentRequest{}).ToolUsage, "openobserve_logs_execute")

	metricsWrapper := newMetricsAgent(nil, "acct", provider).(*metricsAgent)
	assertAgentHasTool(t, metricsWrapper, "openobserve_metrics_execute")
	metricsAgent := metricsWrapper.agent.(*PrometheusAgent)
	assert.Contains(t, metricsAgent.GetSystemPrompt(nil, core.NBAgentRequest{}).ToolUsage, "openobserve_metrics_execute")

	tracesAgent := TracesDefaultAgent{accountId: "acct", provider: provider}
	assertAgentHasTool(t, tracesAgent, "openobserve_traces_execute")
	assert.Contains(t, tracesAgent.GetSystemPrompt(nil, core.NBAgentRequest{}).ToolUsage, "openobserve_traces_execute")

	tracesAgentV2 := newTracesDefaultAgentV2("acct", provider)
	assertAgentHasTool(t, tracesAgentV2, "openobserve_traces_execute")
	assert.Contains(t, tracesAgentV2.GetSystemPrompt(nil, core.NBAgentRequest{}).ToolUsage, "openobserve_traces_execute")

	for _, name := range []string{"openobserve_logs_execute", "openobserve_metrics_execute", "openobserve_traces_execute"} {
		_, ok := toolcore.GetNBTool("acct", name)
		require.Truef(t, ok, "tool factory %q should be registered", name)
	}
}

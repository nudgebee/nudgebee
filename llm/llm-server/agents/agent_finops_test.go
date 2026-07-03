package agents

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFinOpsAgent_RoutesThroughSpecialistAgents pins the fix that moved FinOps off
// the raw prometheus_execute, kubectl_execute, and cloud_resource_search_execute
// tools and onto their specialist agents-as-tools (PrometheusAgentName,
// KubectlAgentName, ResourceSearchAgentName). Each specialist agent carries
// guardrails (PromQL construction, kubectl safety rules, multi-platform resource
// fan-out + relevance filtering) that live only in that agent's own system prompt
// and were invisible to FinOps when it called the raw tool directly.
func TestFinOpsAgent_RoutesThroughSpecialistAgents(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	finOpsAgent := &FinOpsAgent{accountId: accountId}

	supportedTools := finOpsAgent.GetSupportedTools(sc)
	toolNames := make([]string, len(supportedTools))
	for i, tool := range supportedTools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, PrometheusAgentName,
		"FinOps must route metrics questions through the prometheus agent, not call it as a raw tool")
	assert.Contains(t, toolNames, KubectlAgentName,
		"FinOps must route kubectl questions through the kubectl agent, not call it as a raw tool")
	assert.Contains(t, toolNames, ResourceSearchAgentName,
		"FinOps must route resource discovery through the resource_search agent, not call it as a raw tool")

	assert.NotContains(t, toolNames, tools.ToolQueryPrometheus,
		"the raw prometheus_execute tool must not be directly exposed to FinOps anymore")
	assert.NotContains(t, toolNames, tools.ToolExecuteKubectlCommand,
		"the raw kubectl_execute tool must not be directly exposed to FinOps anymore")
	assert.NotContains(t, toolNames, tools.ToolCloudResourceSearch,
		"the raw cloud_resource_search_execute tool must not be directly exposed to FinOps anymore")

	// anomaly_execute has no specialist wrapping agent, so it stays a direct tool.
	assert.Contains(t, toolNames, tools.ToolAnomalyExecuteSql,
		"anomaly_execute has no wrapping agent and should remain a direct tool")
}

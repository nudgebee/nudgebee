package agents

import (
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFinOpsAgent_RoutesThroughSpecialistAgents pins FinOps's tool routing.
// Metrics still routes through its specialist agent (MetricsAgentName) for
// provider routing. Resource discovery now uses the direct
// cloud_resource_search_execute tool — the resource_search agent was removed in
// #32503 Phase 2; its multi-platform + relevance guardrails moved into the tool
// (unified cloud_resourses query + the #36078 relevance filter).
//
// Kubectl was moved off KubectlAgentName and onto tools.ToolExecuteKubectlCommand
// in #32503 Phase 1 to unblock the kubectl-sub-agent removal in Phase 2. The
// kubectl safety rules that used to live in the sub-agent's system prompt still
// need to migrate to KubectlExecuteTool.ToolPrompt() — tracked as a TODO on the
// FinOps tool list.
func TestFinOpsAgent_RoutesThroughSpecialistAgents(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	if accountId == "" {
		accountId = "test-finops-routing"
	}
	finOpsAgent := &FinOpsAgent{accountId: accountId}

	// Mock the AccountConfigSummary so kubectl_execute (which requires k8s
	// configured) passes the IsToolConfigured gate. Same pattern used in
	// the (removed) k8s orchestrator mode tests.
	summaryJson := `{"IntegrationTypes":{},"TenantIntegrationTypes":{},"CloudProviders":{"k8s":true},"HasAgent":true}`
	err := common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, "account_config_summary:v2:"+accountId, []byte(summaryJson))
	assert.NoError(t, err)
	configsJson := `[{"id":"config-1","name":"kubectl_execute"}]`
	err = common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, "list_tool_configs:"+accountId+":"+tools.ToolExecuteKubectlCommand, []byte(configsJson))
	assert.NoError(t, err)

	supportedTools := finOpsAgent.GetSupportedTools(sc)
	toolNames := make([]string, len(supportedTools))
	for i, tool := range supportedTools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, MetricsAgentName,
		"FinOps must route metrics questions through the provider-agnostic metrics agent, not call prometheus_execute as a raw tool")
	assert.Contains(t, toolNames, tools.ToolExecuteKubectlCommand,
		"FinOps calls kubectl_execute directly (#32503 Phase 1); safety-rule migration to ToolPrompt is pending")
	assert.Contains(t, toolNames, tools.ToolCloudResourceSearch,
		"FinOps resolves resources via the direct cloud_resource_search_execute tool (resource_search agent removed, #32503 Phase 2)")

	assert.NotContains(t, toolNames, tools.ToolQueryPrometheus,
		"the raw prometheus_execute tool must not be directly exposed to FinOps anymore")
	assert.NotContains(t, toolNames, KubectlAgentName,
		"FinOps no longer routes kubectl through the sub-agent (#32503 Phase 1)")

	// anomaly_execute has no specialist wrapping agent, so it stays a direct tool.
	assert.Contains(t, toolNames, tools.ToolAnomalyExecuteSql,
		"anomaly_execute has no wrapping agent and should remain a direct tool")
}

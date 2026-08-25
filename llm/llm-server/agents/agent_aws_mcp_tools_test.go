package agents

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	tocore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAwsDebug_IncludesMCPIntegrationTools(t *testing.T) {
	const testAccountId = "test-aws-mcp-tools"

	mockTools := []tocore.NBTool{
		mockMCPTool{name: "mcp_test_server_echo"},
	}
	tocore.SetMCPIntegrationToolCache(testAccountId, mockTools)
	defer tocore.ClearMCPIntegrationToolCache(testAccountId)

	sc := security.NewRequestContextForSuperAdmin()
	// Lean-only orchestrator: MCP inclusion comes via getCloudLeanSupportedTools,
	// which merges ListMCPIntegrationTools into the returned tool set.
	tl := getCloudLeanSupportedTools(sc, testAccountId, AgentAwsOrchestratorName, tools.ToolExecuteAwsCliCommand)
	toolNames := make([]string, len(tl))
	for i, tool := range tl {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "mcp_test_server_echo",
		"aws orchestrator should include MCP integration tools")
}

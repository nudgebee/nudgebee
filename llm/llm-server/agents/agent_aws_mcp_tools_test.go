package agents

import (
	"nudgebee/llm/security"
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
	tools := getAwsPlannerSupportedTools(sc, testAccountId)
	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "mcp_test_server_echo",
		"aws_debug agent should include MCP integration tools")
}

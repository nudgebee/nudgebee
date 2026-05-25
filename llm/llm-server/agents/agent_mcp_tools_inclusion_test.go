package agents

import (
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockMCPTool is a minimal NBTool for testing MCP tool inclusion in agents.
type mockMCPTool struct {
	name string
}

func (m mockMCPTool) Name() string                     { return m.name }
func (m mockMCPTool) Description() string              { return "mock MCP tool" }
func (m mockMCPTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (m mockMCPTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (m mockMCPTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

func TestAllAgents_IncludeMCPIntegrationTools(t *testing.T) {
	const testAccountId = "test-mcp-tools-inclusion"
	sc := security.NewRequestContextForSuperAdmin()

	// Pre-populate the MCP integration tool cache with a mock tool
	mockTools := []toolcore.NBTool{
		mockMCPTool{name: "mcp_test_server_echo"},
		mockMCPTool{name: "mcp_test_server_list"},
	}
	toolcore.SetMCPIntegrationToolCache(testAccountId, mockTools)
	defer toolcore.ClearMCPIntegrationToolCache(testAccountId)

	tests := []struct {
		name     string
		getTools func() []toolcore.NBTool
	}{
		{
			name: "k8s_debug",
			getTools: func() []toolcore.NBTool {
				return getSupportedTools(sc, testAccountId, "k8s_debug")
			},
		},
		{
			name: "gcp_debug",
			getTools: func() []toolcore.NBTool {
				return getGcpPlannerSupportedTools(sc, testAccountId)
			},
		},
		{
			name: "azure_debug",
			getTools: func() []toolcore.NBTool {
				return getAzurePlannerSupportedTools(sc, testAccountId)
			},
		},
		{
			name: "datadog_debug",
			getTools: func() []toolcore.NBTool {
				return getDatadogPlannerSupportedTools(sc, testAccountId)
			},
		},
		{
			name: "argocd",
			getTools: func() []toolcore.NBTool {
				agent := ArgoCDAgent{accountId: testAccountId}
				return agent.GetSupportedTools(sc)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tools := tc.getTools()
			toolNames := make([]string, len(tools))
			for i, tool := range tools {
				toolNames[i] = tool.Name()
			}

			assert.Contains(t, toolNames, "mcp_test_server_echo",
				"agent %s should include MCP integration tool 'mcp_test_server_echo'", tc.name)
			assert.Contains(t, toolNames, "mcp_test_server_list",
				"agent %s should include MCP integration tool 'mcp_test_server_list'", tc.name)
		})
	}
}

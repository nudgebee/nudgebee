package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/tools/core"
)

// The Tools list asks for tools only; agents are on the Agents tab (#40399).
// Without nb_tool_type every entry comes back, as agent pickers need.
func TestFilterToolsByType(t *testing.T) {
	tools := []core.ToolDto{{Name: "kubectl_execute", NBToolType: core.NBToolTypeTool}, {Name: "llm", NBToolType: core.NBToolTypeAgent}}

	kept := filterToolsByType(tools, map[string]any{"nb_tool_type": "tool"})
	assert.Equal(t, []string{"kubectl_execute"}, []string{kept[0].Name})
	assert.Len(t, kept, 1)
	assert.Len(t, filterToolsByType(tools, map[string]any{}), 2)
	assert.Len(t, filterToolsByType(tools, map[string]any{"nb_tool_type": " "}), 2)
	assert.Equal(t, "llm", tools[1].Name, "the given list, which can be the cache's, is left as it was")
}

// Write-marked MCP tools reach the Tool Approvals picker as integration tools
// from MCP, so the editor offers entire-tool and argument rules only.
func TestToolApprovalMCPToolOptionsAreMCPIntegrationTools(t *testing.T) {
	options := toolApprovalMCPToolOptions([]mcpWriteTool{{Name: "jira_create_issue"}})
	assert.Len(t, options, 1)
	assert.Equal(t, "mcp", options[0].Source)
	assert.Equal(t, toolApprovalTargetIntegration, options[0].TargetKind)
}

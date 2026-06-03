package agents

import (
	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDelegateAgent_NonDebugAgentDoesNotUseDelegateAgent verifies that a non-debug
// agent (e.g., postgres) does not have delegate_agent in its tool list.
func TestDelegateAgent_NonDebugAgentDoesNotUseDelegateAgent(t *testing.T) {
	// Create a postgres agent (a non-debug, leaf agent)
	agent, ok := core.GetNBAgent(nil, PostgresAgentName, "fake-account-id", core.AgentStatusEnabled)
	if !ok {
		t.Skip("postgres agent not available in test environment")
	}

	tools := agent.GetSupportedTools(nil)
	for _, tool := range tools {
		assert.NotEqual(t, DelegateAgentToolName, tool.Name(),
			"Non-debug agent %s should NOT have delegate_agent in its tools", agent.GetName())
	}
}

// TestDelegateAgent_ToolRegistration verifies delegate_agent is registered in the
// global tool registry and can be resolved.
func TestDelegateAgent_ToolRegistration(t *testing.T) {
	tool, ok := toolcore.GetNBTool("fake-account-id", DelegateAgentToolName)
	assert.True(t, ok, "delegate_agent should be registered in the tool registry")
	assert.NotNil(t, tool)
	assert.Equal(t, DelegateAgentToolName, tool.Name())
	assert.Equal(t, toolcore.NBToolTypeTool, tool.GetType())
}

// TestDelegateAgent_HasDelegateAgentTool verifies the template conditional helper.
func TestDelegateAgent_HasDelegateAgentTool(t *testing.T) {
	withDelegate := []toolcore.NBTool{
		&mockTool{name: "kubectl"},
		&mockTool{name: DelegateAgentToolName},
		&mockTool{name: "postgres"},
	}
	assert.True(t, core.HasDelegateAgentTool(withDelegate))

	withoutDelegate := []toolcore.NBTool{
		&mockTool{name: "kubectl"},
		&mockTool{name: "postgres"},
	}
	assert.False(t, core.HasDelegateAgentTool(withoutDelegate))

	assert.False(t, core.HasDelegateAgentTool(nil))
}

package core

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupToolForPlanner_InMap(t *testing.T) {
	mockTool := &MockContextCapturingTool{NameVal: "kubectl_execute"}
	nameToTool := map[string]toolcore.NBTool{
		"KUBECTL_EXECUTE": mockTool,
	}

	// Exact match uppercase
	tool, ok := lookupToolForPlanner(nameToTool, "account-1", "KUBECTL_EXECUTE")
	require.True(t, ok)
	assert.Equal(t, mockTool, tool)

	// Lowercase match
	tool, ok = lookupToolForPlanner(nameToTool, "account-1", "kubectl_execute")
	require.True(t, ok)
	assert.Equal(t, mockTool, tool)

	// With whitespace
	tool, ok = lookupToolForPlanner(nameToTool, "account-1", "  kubectl_execute  ")
	require.True(t, ok)
	assert.Equal(t, mockTool, tool)
}

func TestLookupToolForPlanner_FallbackToSystemTools(t *testing.T) {
	nameToTool := map[string]toolcore.NBTool{}

	// "shell" alias should resolve to ToolExecuteShellCommand and look up system tool
	// If ToolExecuteShellCommand is registered in system tools, it resolves.
	// Let's test with a registered system tool or test fallback when not in map.
	tool, ok := lookupToolForPlanner(nameToTool, "account-1", "non_existent_tool_12345")
	assert.False(t, ok)
	assert.Nil(t, tool)
}

func TestResumeConfirmationKey_Fallback(t *testing.T) {
	mockTool := &MockContextCapturingTool{NameVal: "kubectl_execute"}
	nameToTool := map[string]toolcore.NBTool{
		"KUBECTL_EXECUTE": mockTool,
	}

	action := NBAgentPlannerToolAction{
		Tool:      "kubectl_execute",
		ToolInput: `{"command": "get pods"}`,
	}

	e := &plannerExecutor{agentRequest: NBAgentRequest{AccountId: "account-1"}}
	key := e.confirmationKeyOf(nameToTool, action)
	assert.NotEmpty(t, key)

	// Unknown tool returns tool name
	unknownAction := NBAgentPlannerToolAction{
		Tool:      "unknown_tool",
		ToolInput: `{}`,
	}
	keyUnknown := e.confirmationKeyOf(nameToTool, unknownAction)
	assert.Equal(t, "unknown_tool", keyUnknown)
}

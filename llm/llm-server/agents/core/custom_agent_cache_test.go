package core

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyAgentTestTool struct {
	name string
}

func (d dummyAgentTestTool) Name() string                     { return d.name }
func (d dummyAgentTestTool) Description() string              { return "dummy tool for agent test" }
func (d dummyAgentTestTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (d dummyAgentTestTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (d dummyAgentTestTool) Call(toolcore.NbToolContext, toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

func TestCustomAgentToolsCache_EvictionOnToolCacheInvalidation(t *testing.T) {
	accountID := "acct-test-agent-cache-1"
	agentName := "TestAgent1"

	// Pre-populate custom agent tools cache
	customAgentToolsCacheInst.set(accountID, agentName, []toolcore.NBTool{
		dummyAgentTestTool{name: "ToolA"},
	})

	cached, ok := customAgentToolsCacheInst.get(accountID, agentName)
	require.True(t, ok)
	require.Len(t, cached, 1)

	// Global tool cache invalidation for account must evict custom agent tools cache
	toolcore.InvalidateAllCaches(accountID)

	_, okAfter := customAgentToolsCacheInst.get(accountID, agentName)
	assert.False(t, okAfter, "customAgentToolsCacheInst must be evicted when tool caches are invalidated")
}

func TestCustomAgentToolsCache_EvictionOnAgentCacheInvalidation(t *testing.T) {
	accountID := "acct-test-agent-cache-2"
	agentName := "TestAgent2"

	customAgentToolsCacheInst.set(accountID, agentName, []toolcore.NBTool{
		dummyAgentTestTool{name: "ToolB"},
	})

	cached, ok := customAgentToolsCacheInst.get(accountID, agentName)
	require.True(t, ok)
	require.Len(t, cached, 1)

	// Agent-specific cache invalidation
	InvalidateAllAgentCaches(accountID, agentName)

	_, okAfter := customAgentToolsCacheInst.get(accountID, agentName)
	assert.False(t, okAfter, "customAgentToolsCacheInst must be evicted when agent caches are invalidated")
}

func TestCustomAgent_GetSupportedTools_DoesNotCacheMissingTools(t *testing.T) {
	accountID := "acct-test-agent-cache-3"
	agentName := "Flower_Info_Agent"

	// Ensure clean cache state
	customAgentToolsCacheInst.delete(accountID, agentName)

	agent := &nbCustomAgent{
		accountId: accountID,
		agent: AgentDto{
			Name:  agentName,
			Tools: []string{"NonExistentToolXYZ"},
		},
	}

	tools := agent.GetSupportedTools(nil)
	assert.Empty(t, tools)

	// Verify that incomplete/missing tool resolutions are NOT cached
	_, ok := customAgentToolsCacheInst.get(accountID, agentName)
	assert.False(t, ok, "incomplete toolset must not be stored in customAgentToolsCacheInst")
}

func TestCustomAgent_GetSupportedTools_CachesWhenAllToolsResolved(t *testing.T) {
	accountID := "acct-test-agent-cache-4"
	agentName := "AllResolvedAgent"
	toolName := "dummy_resolved_tool_test"

	toolcore.RegisterNBToolFactory(toolName, func(accountId string) (toolcore.NBTool, error) {
		return dummyAgentTestTool{name: toolName}, nil
	})

	// Ensure clean cache state
	customAgentToolsCacheInst.delete(accountID, agentName)

	agent := &nbCustomAgent{
		accountId: accountID,
		agent: AgentDto{
			Name:  agentName,
			Tools: []string{toolName},
		},
	}

	tools := agent.GetSupportedTools(nil)
	require.Len(t, tools, 1)
	assert.Equal(t, toolName, tools[0].Name())

	// Verify that successfully resolved toolset is cached
	cached, ok := customAgentToolsCacheInst.get(accountID, agentName)
	assert.True(t, ok, "fully resolved toolset must be cached in customAgentToolsCacheInst")
	assert.Len(t, cached, 1)
}

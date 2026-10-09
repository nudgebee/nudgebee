package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

type dummyTestTool struct {
	name string
}

func (d dummyTestTool) Name() string            { return d.name }
func (d dummyTestTool) Description() string     { return "dummy tool for cache test" }
func (d dummyTestTool) InputSchema() ToolSchema { return ToolSchema{} }
func (d dummyTestTool) GetType() NBToolType     { return NBToolTypeTool }
func (d dummyTestTool) Call(NbToolContext, NBToolCallRequest) (NBToolResponse, error) {
	return NBToolResponse{}, nil
}

func TestToolCache_SetGetDelete(t *testing.T) {
	accountID := "test-acct-tool-cache-1"
	tool1 := dummyTestTool{name: "TestFlowerFacts"}

	// Set tool in cache
	toolCacheInstance.set(accountID, []NBTool{tool1})

	// Get from cache (case-insensitive)
	foundTool, ok := toolCacheInstance.getFromCache(accountID, "testflowerfacts")
	assert.True(t, ok)
	assert.Equal(t, "TestFlowerFacts", foundTool.Name())

	foundToolExact, okExact := toolCacheInstance.getFromCache(accountID, "TestFlowerFacts")
	assert.True(t, okExact)
	assert.Equal(t, "TestFlowerFacts", foundToolExact.Name())

	// Delete from cache
	toolCacheInstance.delete(accountID)

	_, okAfterDelete := toolCacheInstance.getFromCache(accountID, "TestFlowerFacts")
	assert.False(t, okAfterDelete)
}

func TestToolCache_InvalidateCachesForAccount(t *testing.T) {
	accountID := "test-acct-tool-cache-2"
	tool1 := dummyTestTool{name: "MyCustomTool"}

	toolCacheInstance.set(accountID, []NBTool{tool1})
	_, ok := toolCacheInstance.getFromCache(accountID, "MyCustomTool")
	assert.True(t, ok)

	// invalidateCachesForAccount must clear toolCacheInstance
	invalidateCachesForAccount(accountID)

	_, okAfter := toolCacheInstance.getFromCache(accountID, "MyCustomTool")
	assert.False(t, okAfter)
}

// #40766: GetEnabledNBTools and ListTools stored a copy of the MCP tool list in
// their own 30-minute entries. A copy taken while an MCP server was down
// outlived the outage by up to half an hour, whatever the MCP cache did. They
// now keep only their own part and read the MCP list on every call.
func TestGetEnabledNBTools_ReadsMCPToolsOnEveryCall(t *testing.T) {
	registerUnavailableMetastore(t)
	const accountId = "test-acct-enabled-tools-mcp"
	ctx := security.NewRequestContextForSuperAdmin()
	t.Cleanup(func() {
		enabledToolsCacheInstance.delete(accountId)
		mcpIntegrationToolCacheInstance.delete(accountId)
	})

	browser := mcpIntegrationConfig{IntegrationID: "int-browser", IntegrationName: "browser"}
	browserDown := []NBTool{newMCPUnavailableTool(browser, "mcp_unreachable")}
	browserUp := []NBTool{mcpIntegrationTool{config: browser, toolName: "browser_navigate"}}

	// Cache miss while the browser server is down: the caller gets the placeholder, the
	// stored entry does not.
	mcpIntegrationToolCacheInstance.set(accountId, browserDown)
	assert.Contains(t, nbToolNames(GetEnabledNBTools(ctx, accountId)), "browser_unavailable")
	stored, ok := enabledToolsCacheInstance.get(accountId)
	assert.True(t, ok)
	assert.NotContains(t, nbToolNames(stored), "browser_unavailable")

	// the browser server is discovered again: the same stored entry now comes back with
	// the real tool.
	mcpIntegrationToolCacheInstance.set(accountId, browserUp)
	names := nbToolNames(GetEnabledNBTools(ctx, accountId))
	assert.Contains(t, names, "browser_navigate")
	assert.NotContains(t, names, "browser_unavailable")
}

// The list handed to a caller is the caller's: planner code appends to tool
// lists, and that must not write into the cached entry.
func TestGetEnabledNBTools_ResultDoesNotAliasTheCachedEntry(t *testing.T) {
	registerUnavailableMetastore(t)
	const accountId = "test-acct-enabled-tools-alias"
	ctx := security.NewRequestContextForSuperAdmin()
	t.Cleanup(func() {
		enabledToolsCacheInstance.delete(accountId)
		mcpIntegrationToolCacheInstance.delete(accountId)
	})

	stored := make([]NBTool, 1, 4)
	stored[0] = dummyTestTool{name: "kubectl"}
	enabledToolsCacheInstance.set(accountId, stored)

	for _, mcpTools := range [][]NBTool{nil, {mcpIntegrationTool{toolName: "browser_navigate"}}} {
		mcpIntegrationToolCacheInstance.set(accountId, mcpTools)
		got := GetEnabledNBTools(ctx, accountId)
		assert.Len(t, got, 1+len(mcpTools))
		_ = append(got, dummyTestTool{name: "appended-by-caller"})
		assert.Nil(t, stored[:2][1], "the cached entry's spare capacity was written to")
	}
}

func TestListTools_ReadsMCPToolsOnEveryCall(t *testing.T) {
	registerUnavailableMetastore(t)
	const accountId = "test-acct-list-tools-mcp"
	ctx := security.NewRequestContextForSuperAdmin()
	t.Cleanup(func() {
		toolDtoCacheInstance.delete(accountId)
		mcpIntegrationToolCacheInstance.delete(accountId)
	})
	dtoNames := func(dtos []ToolDto) []string {
		names := make([]string, 0, len(dtos))
		for _, d := range dtos {
			names = append(names, d.Name)
		}
		return names
	}

	browser := mcpIntegrationConfig{IntegrationID: "int-browser", IntegrationName: "browser"}
	browserDown := []NBTool{newMCPUnavailableTool(browser, "mcp_unreachable")}
	browserUp := []NBTool{mcpIntegrationTool{config: browser, toolName: "browser_navigate"}}

	// Cache miss while the browser server is down: it has no tools to list.
	mcpIntegrationToolCacheInstance.set(accountId, browserDown)
	assert.NotContains(t, dtoNames(ListTools(ctx, accountId)), "browser_navigate")
	_, ok := toolDtoCacheInstance.get(accountId)
	assert.True(t, ok)

	// the browser server is discovered again: its tool is listed without waiting for the
	// stored entry to expire.
	mcpIntegrationToolCacheInstance.set(accountId, browserUp)
	assert.Contains(t, dtoNames(ListTools(ctx, accountId)), "browser_navigate")
	stored, _ := toolDtoCacheInstance.get(accountId)
	assert.NotContains(t, dtoNames(stored), "browser_navigate")
}

// The tool list API appends the account's custom agents to what ListTools
// returns; that must not write into the cached entry either.
func TestListTools_ResultDoesNotAliasTheCachedEntry(t *testing.T) {
	registerUnavailableMetastore(t)
	const accountId = "test-acct-list-tools-alias"
	ctx := security.NewRequestContextForSuperAdmin()
	t.Cleanup(func() {
		toolDtoCacheInstance.delete(accountId)
		mcpIntegrationToolCacheInstance.delete(accountId)
	})

	stored := make([]ToolDto, 1, 4)
	stored[0] = ToolDto{Name: "kubectl"}
	toolDtoCacheInstance.set(accountId, stored)

	for _, mcpTools := range [][]NBTool{nil, {mcpIntegrationTool{toolName: "browser_navigate"}}} {
		mcpIntegrationToolCacheInstance.set(accountId, mcpTools)
		got := ListTools(ctx, accountId)
		assert.Len(t, got, 1+len(mcpTools))
		_ = append(got, ToolDto{Name: "appended-by-caller"})
		assert.Empty(t, stored[:2][1].Name, "the cached entry's spare capacity was written to")
	}
}

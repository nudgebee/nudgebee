package core

import (
	"testing"

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

func TestToolCache_InvalidateLocalCaches(t *testing.T) {
	accountID := "test-acct-tool-cache-2"
	tool1 := dummyTestTool{name: "MyCustomTool"}

	toolCacheInstance.set(accountID, []NBTool{tool1})
	_, ok := toolCacheInstance.getFromCache(accountID, "MyCustomTool")
	assert.True(t, ok)

	// invalidateLocalCaches must clear toolCacheInstance
	invalidateLocalCaches(accountID)

	_, okAfter := toolCacheInstance.getFromCache(accountID, "MyCustomTool")
	assert.False(t, okAfter)
}

package core

// SetMCPIntegrationToolCache populates the MCP tool cache for testing.
func SetMCPIntegrationToolCache(accountId string, tools []NBTool) {
	mcpIntegrationToolCacheInstance.set(accountId, tools)
}

// ClearMCPIntegrationToolCache removes the MCP tool cache entry for testing.
func ClearMCPIntegrationToolCache(accountId string) {
	mcpIntegrationToolCacheInstance.delete(accountId)
}

// GetMCPIntegrationToolCache returns the cached MCP tools for an account
// without falling through to a DB load. Returns (nil, false) when the
// cache is empty or the entry has expired. Test-only helper used to
// verify invalidation actually clears the cache rather than re-loading
// it.
func GetMCPIntegrationToolCache(accountId string) ([]NBTool, bool) {
	return mcpIntegrationToolCacheInstance.get(accountId)
}

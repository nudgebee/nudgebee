package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUnwrapSSEIfPresent(t *testing.T) {
	const json = `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain JSON object passes through",
			in:   json,
			want: json,
		},
		{
			name: "plain JSON array passes through",
			in:   `[1,2,3]`,
			want: `[1,2,3]`,
		},
		{
			name: "leading whitespace before JSON still passes through",
			in:   "  \n  " + json,
			want: "  \n  " + json,
		},
		{
			name: "single SSE event with one data line",
			in:   "event: message\ndata: " + json + "\n\n",
			want: json,
		},
		{
			name: "SSE without explicit event line (server may omit it)",
			in:   "data: " + json + "\n\n",
			want: json,
		},
		{
			name: "multiple SSE events — last data wins (final result)",
			in: "event: progress\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"progress\"}\n\n" +
				"event: message\ndata: " + json + "\n\n",
			want: json,
		},
		{
			name: "CRLF line endings",
			in:   "event: message\r\ndata: " + json + "\r\n\r\n",
			want: json,
		},
		{
			name: "non-SSE non-JSON returned as-is so the JSON parser surfaces the real error",
			in:   "<html>oh no</html>",
			want: "<html>oh no</html>",
		},
		{
			name: "empty body",
			in:   "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, unwrapSSEIfPresent(tc.in))
		})
	}
}

func TestInvalidateAccountIntegrationCache_RemovesCachedTools(t *testing.T) {
	const accountId = "acct-mcp-invalidate-test"

	cached := []NBTool{
		mcpIntegrationTool{
			toolName:    "stub_tool",
			mcpToolName: "tool",
			toolDesc:    "stub",
		},
	}
	mcpIntegrationToolCacheInstance.set(accountId, cached)

	tools, ok := mcpIntegrationToolCacheInstance.get(accountId)
	assert.True(t, ok, "cache should be populated before invalidation")
	assert.Len(t, tools, 1)

	InvalidateAccountIntegrationCache(accountId)

	_, ok = mcpIntegrationToolCacheInstance.get(accountId)
	assert.False(t, ok, "cache entry should be removed after invalidation")
}

func TestInvalidateAccountIntegrationCache_UnknownAccountIsNoOp(t *testing.T) {
	const accountId = "acct-mcp-unknown"

	_, ok := mcpIntegrationToolCacheInstance.get(accountId)
	assert.False(t, ok, "precondition: cache must be empty for unknown account")

	assert.NotPanics(t, func() {
		InvalidateAccountIntegrationCache(accountId)
	})

	_, ok = mcpIntegrationToolCacheInstance.get(accountId)
	assert.False(t, ok, "cache should remain empty for unknown account")
}

func TestInvalidateAccountIntegrationCache_OnlyAffectsTargetAccount(t *testing.T) {
	const targetAccount = "acct-target"
	const otherAccount = "acct-other"

	stub := []NBTool{mcpIntegrationTool{toolName: "stub", mcpToolName: "stub"}}
	mcpIntegrationToolCacheInstance.set(targetAccount, stub)
	mcpIntegrationToolCacheInstance.set(otherAccount, stub)

	InvalidateAccountIntegrationCache(targetAccount)

	_, ok := mcpIntegrationToolCacheInstance.get(targetAccount)
	assert.False(t, ok, "target account's cache must be cleared")

	_, ok = mcpIntegrationToolCacheInstance.get(otherAccount)
	assert.True(t, ok, "other account's cache must remain populated")

	mcpIntegrationToolCacheInstance.delete(otherAccount)
}

func TestMCPToolsAvailableSnapshot_GroupsByIntegration(t *testing.T) {
	// Wipe the cache so cross-test pollution doesn't leak in.
	mcpIntegrationToolCacheInstance.mutex.Lock()
	mcpIntegrationToolCacheInstance.data = make(map[string]struct {
		tools  []NBTool
		expiry time.Time
	})
	mcpIntegrationToolCacheInstance.mutex.Unlock()
	t.Cleanup(func() {
		mcpIntegrationToolCacheInstance.mutex.Lock()
		mcpIntegrationToolCacheInstance.data = make(map[string]struct {
			tools  []NBTool
			expiry time.Time
		})
		mcpIntegrationToolCacheInstance.mutex.Unlock()
	})

	// Two MCP integrations under one account, plus another account with one
	// integration. Snapshot must report three (account, integration) rows
	// with the right counts.
	mcpIntegrationToolCacheInstance.set("acc-A", []NBTool{
		mcpIntegrationTool{config: mcpIntegrationConfig{IntegrationID: "int-1"}, mcpToolName: "t1"},
		mcpIntegrationTool{config: mcpIntegrationConfig{IntegrationID: "int-1"}, mcpToolName: "t2"},
		mcpIntegrationTool{config: mcpIntegrationConfig{IntegrationID: "int-2"}, mcpToolName: "t3"},
	})
	mcpIntegrationToolCacheInstance.set("acc-B", []NBTool{
		mcpIntegrationTool{config: mcpIntegrationConfig{IntegrationID: "int-1"}, mcpToolName: "t1"},
	})

	snap := mcpToolsAvailableSnapshot()

	type key struct{ acc, intg string }
	got := make(map[key]int, len(snap))
	for _, e := range snap {
		got[key{e.AccountId, e.IntegrationId}] = e.ToolCount
	}

	assert.Equal(t, 2, got[key{"acc-A", "int-1"}])
	assert.Equal(t, 1, got[key{"acc-A", "int-2"}])
	assert.Equal(t, 1, got[key{"acc-B", "int-1"}])
	assert.Len(t, got, 3, "snapshot must contain exactly the three (account, integration) groups")
}

func TestMCPToolsAvailableSnapshot_SkipsExpiredEntries(t *testing.T) {
	mcpIntegrationToolCacheInstance.mutex.Lock()
	mcpIntegrationToolCacheInstance.data = map[string]struct {
		tools  []NBTool
		expiry time.Time
	}{
		"acc-fresh": {
			tools: []NBTool{
				mcpIntegrationTool{config: mcpIntegrationConfig{IntegrationID: "int-fresh"}, mcpToolName: "t"},
			},
			expiry: time.Now().Add(time.Hour),
		},
		"acc-stale": {
			tools: []NBTool{
				mcpIntegrationTool{config: mcpIntegrationConfig{IntegrationID: "int-stale"}, mcpToolName: "t"},
			},
			expiry: time.Now().Add(-time.Hour),
		},
	}
	mcpIntegrationToolCacheInstance.mutex.Unlock()
	t.Cleanup(func() {
		mcpIntegrationToolCacheInstance.mutex.Lock()
		mcpIntegrationToolCacheInstance.data = make(map[string]struct {
			tools  []NBTool
			expiry time.Time
		})
		mcpIntegrationToolCacheInstance.mutex.Unlock()
	})

	snap := mcpToolsAvailableSnapshot()
	for _, e := range snap {
		assert.NotEqual(t, "acc-stale", e.AccountId, "expired entries must not be reported")
	}
}

func TestMCPToolsAvailableSnapshot_EmptyCacheReturnsEmpty(t *testing.T) {
	mcpIntegrationToolCacheInstance.mutex.Lock()
	mcpIntegrationToolCacheInstance.data = make(map[string]struct {
		tools  []NBTool
		expiry time.Time
	})
	mcpIntegrationToolCacheInstance.mutex.Unlock()

	snap := mcpToolsAvailableSnapshot()
	assert.Empty(t, snap)
}

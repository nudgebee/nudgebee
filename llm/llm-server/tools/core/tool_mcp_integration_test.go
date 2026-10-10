package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/relay"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	registerUnavailableMetastore(t)
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
	registerUnavailableMetastore(t)
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
	registerUnavailableMetastore(t)
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

func nbToolNames(tools []NBTool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return names
}

// passMCPCacheTime lets d more time pass for the account, as far as the MCP tool
// cache can tell: it moves the entry's expiry, and the start of every run of
// failed discoveries, back by d.
func passMCPCacheTime(accountId string, d time.Duration) {
	c := mcpIntegrationToolCacheInstance
	c.mutex.Lock()
	defer c.mutex.Unlock()
	item := c.data[accountId]
	item.expiry = item.expiry.Add(-d)
	c.data[accountId] = item
	for id, since := range c.failingSince[accountId] {
		c.failingSince[accountId][id] = since.Add(-d)
	}
}

func mcpCacheExpiresIn(accountId string) time.Duration {
	c := mcpIntegrationToolCacheInstance
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return time.Until(c.data[accountId].expiry)
}

func mcpCacheFailingIntegrations(accountId string) []string {
	c := mcpIntegrationToolCacheInstance
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	ids := make([]string, 0, len(c.failingSince[accountId]))
	for id := range c.failingSince[accountId] {
		ids = append(ids, id)
	}
	return ids
}

// #40766: when tools/list failed for one integration, the account's list (the
// other integrations' tools plus that integration's placeholder) was cached for
// the full 30 minutes. A one-minute restart of an MCP server cost the pod that
// integration's tools for half an hour.
func TestListMCPIntegrationTools_RetriesFailedDiscoveryAfterAMinute(t *testing.T) {
	const accountId = "acct-mcp-discovery-retry"
	t.Cleanup(func() { mcpIntegrationToolCacheInstance.delete(accountId) })

	// Two enabled integrations; one config query per cache refill.
	mock := registerMockMetastore(t)
	expectRefill := func() {
		mock.ExpectQuery(`FROM integrations i`).WithArgs(accountId).WillReturnRows(
			sqlmock.NewRows([]string{"id", "name", "config_name", "config_value", "config_encrypted"}).
				AddRow("int-browser", "browser", nil, nil, nil).
				AddRow("int-metrics", "metrics", nil, nil, nil))
	}

	// The relay answers tools/list for both, except for the browser server while it is down.
	var mu sync.Mutex
	browserDown := true
	discoveries := map[string]int{}
	relayStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req relay.RelayExecuteRequest
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		id, _ := req.Body.ActionParams["integration_id"].(string)
		mu.Lock()
		discoveries[id]++
		down := id == "int-browser" && browserDown
		mu.Unlock()
		if down {
			http.Error(w, `{"error":"MCP server request failed: connect: connection refused"}`, http.StatusBadGateway)
			return
		}
		tool := map[string]string{"int-browser": "navigate", "int-metrics": "query"}[id]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 200,
			"data":        fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":%q,"inputSchema":{"type":"object"}}]}}`, tool),
		})
	}))
	defer relayStub.Close()
	orig := config.Config.RelayServerEndpoint
	config.Config.RelayServerEndpoint = relayStub.URL
	defer func() { config.Config.RelayServerEndpoint = orig }()
	discoveryCounts := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		return map[string]int{"int-browser": discoveries["int-browser"], "int-metrics": discoveries["int-metrics"]}
	}

	// The cache refills while the browser server is down.
	expectRefill()
	assert.Equal(t, []string{"browser_unavailable", "metrics_query"}, nbToolNames(ListMCPIntegrationTools(accountId)))
	assert.InDelta(t, time.Minute.Seconds(), mcpCacheExpiresIn(accountId).Seconds(), 5,
		"a list holding a failed discovery must be kept for a minute, not for the 30-minute TTL")

	// the browser server comes back. Inside that minute the cached list is still served
	// and nothing is discovered again.
	mu.Lock()
	browserDown = false
	mu.Unlock()
	passMCPCacheTime(accountId, 50*time.Second)
	assert.Equal(t, []string{"browser_unavailable", "metrics_query"}, nbToolNames(ListMCPIntegrationTools(accountId)))
	assert.Equal(t, map[string]int{"int-browser": 1, "int-metrics": 1}, discoveryCounts())

	// Once the minute is over the next lookup discovers again and gets the real tools.
	passMCPCacheTime(accountId, 15*time.Second)
	expectRefill()
	assert.Equal(t, []string{"browser_navigate", "metrics_query"}, nbToolNames(ListMCPIntegrationTools(accountId)))
	assert.Equal(t, map[string]int{"int-browser": 2, "int-metrics": 2}, discoveryCounts())
	assert.InDelta(t, defaultCacheTTL.Seconds(), mcpCacheExpiresIn(accountId).Seconds(), 5,
		"a list with no failed discovery is kept for the full TTL again")
	assert.Empty(t, mcpCacheFailingIntegrations(accountId))
	require.NoError(t, mock.ExpectationsWereMet())
}

// An account with no MCP integration used to run the config query on every
// lookup: "none" came back as nil, and nil is never cached.
func TestListMCPIntegrationTools_CachesAnAccountWithNoIntegrations(t *testing.T) {
	const accountId = "acct-mcp-no-integrations"
	t.Cleanup(func() { mcpIntegrationToolCacheInstance.delete(accountId) })
	columns := []string{"id", "name", "config_name", "config_value", "config_encrypted"}

	// One config query only: a second one would have no expectation to match
	// and fail, and that failure is never cached.
	mock := registerMockMetastore(t)
	mock.ExpectQuery(`FROM integrations i`).WithArgs(accountId).WillReturnRows(sqlmock.NewRows(columns))

	for range 2 {
		assert.Empty(t, ListMCPIntegrationTools(accountId))
		_, cached := mcpIntegrationToolCacheInstance.get(accountId)
		assert.True(t, cached, "an account with no integrations must be cached")
	}
	assert.InDelta(t, defaultCacheTTL.Seconds(), mcpCacheExpiresIn(accountId).Seconds(), 5)
	require.NoError(t, mock.ExpectationsWereMet())

	// Adding an integration invalidates the account, so the next lookup reads again.
	InvalidateAccountIntegrationCache(accountId)
	_, cached := mcpIntegrationToolCacheInstance.get(accountId)
	assert.False(t, cached)
}

// When the integrations cannot be read there is no answer to cache: the next
// lookup must ask again, not serve "none" for 30 minutes.
func TestListMCPIntegrationTools_DoesNotCacheAFailedConfigRead(t *testing.T) {
	const accountId = "acct-mcp-config-read-fails"
	t.Cleanup(func() { mcpIntegrationToolCacheInstance.delete(accountId) })
	columns := []string{"id", "name", "config_name", "config_value", "config_encrypted"}

	mock := registerMockMetastore(t)
	mock.ExpectQuery(`FROM integrations i`).WithArgs(accountId).WillReturnError(assert.AnError)
	assert.Nil(t, ListMCPIntegrationTools(accountId))
	_, cached := mcpIntegrationToolCacheInstance.get(accountId)
	assert.False(t, cached, "a failed read must not be cached")

	mock.ExpectQuery(`FROM integrations i`).WithArgs(accountId).WillReturnRows(sqlmock.NewRows(columns))
	assert.NotNil(t, ListMCPIntegrationTools(accountId))
	_, cached = mcpIntegrationToolCacheInstance.get(accountId)
	assert.True(t, cached)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An integration that stays down must not be discovered every minute for good:
// the wait before each retry is the time it has already been down, up to the
// normal 30 minutes.
func TestMCPIntegrationToolCache_RetryWaitFollowsTimeDown(t *testing.T) {
	const accountId = "acct-mcp-retry-wait"
	c := mcpIntegrationToolCacheInstance
	t.Cleanup(func() { c.delete(accountId) })

	healthy := mcpIntegrationTool{config: mcpIntegrationConfig{IntegrationID: "int-ok"}, toolName: "ok_tool"}
	aDown := newMCPUnavailableTool(mcpIntegrationConfig{IntegrationID: "int-a", IntegrationName: "a"}, "mcp_unreachable")
	bDown := newMCPUnavailableTool(mcpIntegrationConfig{IntegrationID: "int-b", IntegrationName: "b"}, "mcp_unreachable")
	second := time.Second.Seconds()

	assert.Equal(t, time.Minute, c.set(accountId, []NBTool{healthy, aDown}), "first failure")
	// Several requests can miss the cache at once, and each stores its own
	// result. That must not push the first retry out.
	assert.Equal(t, time.Minute, c.set(accountId, []NBTool{healthy, aDown}), "same failure stored by a second request")

	passMCPCacheTime(accountId, 10*time.Minute)
	assert.InDelta(t, (10 * time.Minute).Seconds(), c.set(accountId, []NBTool{healthy, aDown}).Seconds(), second, "still down after 10 minutes")

	passMCPCacheTime(accountId, 3*time.Hour)
	assert.Equal(t, defaultCacheTTL, c.set(accountId, []NBTool{healthy, aDown}), "down for hours: no more often than a healthy list")

	// A second integration fails for the first time. Its short retry applies,
	// whatever the first one's history.
	assert.Equal(t, time.Minute, c.set(accountId, []NBTool{healthy, aDown, bDown}), "new failure next to an old one")
	assert.ElementsMatch(t, []string{"int-a", "int-b"}, mcpCacheFailingIntegrations(accountId))

	// Both recover, then one fails again: the earlier failures are forgotten.
	assert.Equal(t, defaultCacheTTL, c.set(accountId, []NBTool{healthy}), "all discoveries succeeded")
	assert.Empty(t, mcpCacheFailingIntegrations(accountId))
	assert.Equal(t, time.Minute, c.set(accountId, []NBTool{healthy, aDown}), "first failure after a recovery")

	// An integration change invalidates the account: that starts over too.
	passMCPCacheTime(accountId, 10*time.Minute)
	c.delete(accountId)
	assert.Equal(t, time.Minute, c.set(accountId, []NBTool{healthy, aDown}), "first failure after an invalidation")
}

func TestMCPDiscoveryRetryAfter(t *testing.T) {
	cases := []struct {
		failingFor time.Duration
		want       time.Duration
	}{
		{0, time.Minute},
		{20 * time.Second, time.Minute},
		{time.Minute, time.Minute},
		{4 * time.Minute, 4 * time.Minute},
		{defaultCacheTTL, defaultCacheTTL},
		{6 * time.Hour, defaultCacheTTL},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, mcpDiscoveryRetryAfter(tc.failingFor), "failing for %s", tc.failingFor)
	}
}

// Every MCP tool and custom container tool reports that its result can change,
// so the planner runs a repeated identical call instead of reusing the first
// result (#39635).
func TestMCPTools_ResultCanChange(t *testing.T) {
	assert.True(t, ResultCanChange(mcpIntegrationTool{toolName: "qa_browser_snapshot"}))
	assert.True(t, ResultCanChange(nbCustomMCPTool{}))
	assert.True(t, ResultCanChange(nbCustomContainerTool{}))
	assert.False(t, ResultCanChange(nil))
	assert.False(t, ResultCanChange(newMCPUnavailableTool(mcpIntegrationConfig{}, "x")))
}

package runtime

import (
	"context"
	"testing"

	"nudgebee/llm/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// These tests guard the invariant that the tool-hash tests alone cannot:
// Google AI keeps ONE CachedContent slot per cache key, so anything that
// changes the content hash of a long-lived (account-scope) entry must also
// change its key. Before #42591, ReAct4's per-request tool sets changed the
// hash (llm_cache_toolhash_test.go) but not the key, so alternating requests
// for the same account/agent evicted each other's slot on every call
// (content_changed invalidations) and nothing failed.

var obj = map[string]any{"type": "object"}

func invariantRequest(tools []llms.Tool) *CacheRequest {
	return &CacheRequest{
		Provider:  "googleai",
		Model:     "gemini-2.5-flash",
		AccountId: "acc-invariant",
		AgentName: "k8s_orchestrator",
		Scope:     CacheScopeAccount,
		Tools:     tools,
	}
}

func TestGoogleAICacheKey_HashChangingToolsChangeAccountKey(t *testing.T) {
	msgs := msgsFor("same system prompt")
	cases := map[string][2][]llms.Tool{
		"tool-less vs tools":      {nil, {toolFor("kubectl", obj)}},
		"different tool sets":     {{toolFor("kubectl", obj)}, {toolFor("kubectl", obj), toolFor("update_notebook", obj)}},
		"same name, other schema": {{toolFor("kubectl", obj)}, {toolFor("kubectl", map[string]any{"type": "array"})}},
	}
	for name, tools := range cases {
		t.Run(name, func(t *testing.T) {
			a, b := invariantRequest(tools[0]), invariantRequest(tools[1])
			require.NotEqual(t, hashContent(msgs, a.Tools), hashContent(msgs, b.Tools),
				"precondition: the tools must change the content hash")
			assert.NotEqual(t, googleAICacheKey(a), googleAICacheKey(b),
				"a hash-changing tool set must get its own account-scope slot, or alternating requests thrash it")
		})
	}
}

func TestGoogleAICacheKey_SameToolsAnyOrderShareSlot(t *testing.T) {
	a := invariantRequest([]llms.Tool{toolFor("kubectl", obj), toolFor("update_notebook", obj)})
	b := invariantRequest([]llms.Tool{toolFor("update_notebook", obj), toolFor("kubectl", obj)})
	assert.Equal(t, googleAICacheKey(a), googleAICacheKey(b))
}

// Tool-less callers (react_3, summarizers) must keep the key they had before
// tools joined it, so a deploy does not orphan their live caches.
func TestGoogleAICacheKey_ToolLessKeyUnchanged(t *testing.T) {
	req := invariantRequest(nil)
	want := generateCacheKey(CacheScopeAccount, req.AccountId, "", req.AgentName, req.Model, credsFingerprint("", "", "", "", "", "", ""))
	assert.Equal(t, want, googleAICacheKey(req))
}

// InvalidateCache must find the slot ApplyCache wrote for the same request.
// The cache-error recovery path in llm_common.go used to build its request
// without Tools, so for tool-bearing requests it looked up the tool-less key,
// missed, and the stale CachedContent pointer kept being served until TTL.
func TestGoogleAICacheProvider_InvalidateFindsToolBearingSlot(t *testing.T) {
	p := NewGoogleAICacheProvider()
	req := invariantRequest([]llms.Tool{toolFor("kubectl", obj)})
	key := googleAICacheKey(req)

	// A non-JSON payload makes InvalidateCache delete the shared entry and
	// return before it would call the Google AI API.
	require.NoError(t, common.CacheSet(p.namespace, key, []byte("stale")))
	require.NoError(t, p.InvalidateCache(context.Background(), req))

	_, stillThere := common.CacheGet(p.namespace, key)
	assert.False(t, stillThere, "invalidation must drop the entry stored under the tool-bearing key")
}

func TestCacheRequestTools(t *testing.T) {
	tools := []llms.Tool{toolFor("kubectl", obj)}
	assert.Equal(t, tools, cacheRequestTools([]llms.CallOption{llms.WithTemperature(0.1), llms.WithTools(tools)}))
	assert.Empty(t, cacheRequestTools([]llms.CallOption{llms.WithTemperature(0.1)}))
	assert.Empty(t, cacheRequestTools(nil))
}

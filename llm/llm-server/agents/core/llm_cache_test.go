package core

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// TestGoogleAICacheProvider_SingleflightCollapsesConcurrentCreates verifies the
// fix for #302: concurrent cache-miss creations for the same cache key must
// collapse into a single createCache execution (via the provider's
// singleflight group) so parallel conversations don't each create a distinct,
// duplicate Google AI CachedContent. We exercise the provider's createGroup
// directly with a counting closure — the real createCache hits Google AI and
// can't run in CI — which guards that the group field is wired and dedups
// same-key concurrent calls. Run with -race to catch field races.
func TestGoogleAICacheProvider_SingleflightCollapsesConcurrentCreates(t *testing.T) {
	p := &GoogleAICacheProvider{namespace: "test_singleflight"}

	const cacheKey = "account:agent:model"
	const goroutines = 25
	var createCalls int32

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all at once to maximize contention
			_, _, _ = p.createGroup.Do(cacheKey, func() (interface{}, error) {
				atomic.AddInt32(&createCalls, 1)
				// Hold the slot briefly so the other goroutines coalesce onto it.
				time.Sleep(20 * time.Millisecond)
				return "created", nil
			})
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&createCalls),
		"concurrent same-key cache creations must collapse to a single createCache call")
}

// TestGoogleAICacheProvider_SingleflightAllowsDistinctKeys confirms different
// cache keys are NOT collapsed — each distinct key runs its own creation.
func TestGoogleAICacheProvider_SingleflightAllowsDistinctKeys(t *testing.T) {
	p := &GoogleAICacheProvider{namespace: "test_singleflight"}

	var createCalls int32
	var wg sync.WaitGroup
	keys := []string{"a:1:m", "b:2:m", "c:3:m"}
	for _, k := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, _, _ = p.createGroup.Do(key, func() (interface{}, error) {
				atomic.AddInt32(&createCalls, 1)
				return "created", nil
			})
		}(k)
	}
	wg.Wait()

	assert.Equal(t, int32(len(keys)), atomic.LoadInt32(&createCalls),
		"distinct cache keys must each run their own creation")
}

// TestGoogleAICacheProvider_ReadSharedCacheInfo covers the Tier-2 reuse path
// (#302): a CacheInfo published by another goroutine/replica is read back so we
// reuse it instead of creating a duplicate; a missing/absent key returns nil.
func TestGoogleAICacheProvider_ReadSharedCacheInfo(t *testing.T) {
	p := NewGoogleAICacheProvider()

	info := &CacheInfo{CacheName: "cachedContents/abc", AccountId: "acct-1"}
	data, err := json.Marshal(info)
	assert.NoError(t, err)
	assert.NoError(t, common.CacheSet(p.namespace, "shared-key-1", data))

	got := p.readSharedCacheInfo("shared-key-1")
	if assert.NotNil(t, got, "published CacheInfo must be readable") {
		assert.Equal(t, "cachedContents/abc", got.CacheName)
	}
	assert.Nil(t, p.readSharedCacheInfo("missing-key"), "absent key must return nil")
}

// TestGoogleAICacheProvider_WaitForSharedCacheInfo verifies a non-holder waits
// for the lock holder to publish a USABLE entry (then reuses), skips a stale
// pointer left behind in the shared cache, and times out cleanly otherwise.
func TestGoogleAICacheProvider_WaitForSharedCacheInfo(t *testing.T) {
	p := NewGoogleAICacheProvider()
	const hash = "hash-abc"

	// Never published -> times out -> nil (no panic, no hang).
	assert.Nil(t, p.waitForSharedCacheInfo("never-key", hash, 200*time.Millisecond))

	// A stale pointer (expired + different content) is already present. It must
	// be skipped, not returned, so the waiter times out instead of handing back a
	// CachedContentName that would 403 at GenerateContent time.
	stale, err := json.Marshal(&CacheInfo{
		CacheName:   "cachedContents/stale",
		ContentHash: "hash-OLD",
		ExpiresAt:   time.Now().Add(-time.Hour),
	})
	assert.NoError(t, err)
	assert.NoError(t, common.CacheSet(p.namespace, "stale-key", stale))
	assert.Nil(t, p.waitForSharedCacheInfo("stale-key", hash, 200*time.Millisecond),
		"a stale (expired/mismatched) pointer must be skipped, not returned")

	// Published shortly after the wait starts, matching content and unexpired
	// -> returned.
	data, err := json.Marshal(&CacheInfo{
		CacheName:   "cachedContents/xyz",
		ContentHash: hash,
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	assert.NoError(t, err)
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = common.CacheSet(p.namespace, "late-key", data)
	}()
	got := p.waitForSharedCacheInfo("late-key", hash, 3*time.Second)
	if assert.NotNil(t, got, "must return the usable entry published during the wait") {
		assert.Equal(t, "cachedContents/xyz", got.CacheName)
	}
}

// TestCacheTryLock_NonRedisAlwaysAcquires: with the default (bigcache) provider
// there are no peer replicas, so the Tier-2 lock is a no-op that always
// acquires with an empty token and unlock is safe.
func TestCacheTryLock_NonRedisAlwaysAcquires(t *testing.T) {
	NewGoogleAICacheProvider() // ensure a (bigcache) cache manager exists
	token, acquired := common.CacheTryLock(context.Background(), "lock-key", time.Second)
	assert.True(t, acquired, "non-redis provider must always acquire")
	assert.Equal(t, "", token, "non-redis acquire returns an empty token")
	common.CacheUnlock(context.Background(), "lock-key", token) // must not panic
}

func TestAnthropicCacheProvider_MultiBreakpoint(t *testing.T) {
	p := NewAnthropicCacheProvider()

	messages := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: "System Instructions: Base Agent Rules"}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "Turn 1: Check pod status"}},
		},
		{
			Role:  llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{llms.TextContent{Text: "Turn 1 Response: Checking pods..."}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "Turn 2: Show logs"}},
		},
		{
			Role:  llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{llms.TextContent{Text: "Turn 2 Response: Here are the logs..."}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "Turn 3: Active query"}},
		},
	}

	req := &CacheRequest{
		Provider:       "anthropic",
		Model:          "claude-3-7-sonnet",
		AccountId:      "acc-1",
		ConversationId: "conv-1",
		Messages:       messages,
		Scope:          CacheScopeConversation,
	}

	resp := p.ApplyCache(context.Background(), req)
	assert.NoError(t, resp.Error)
	assert.False(t, resp.CacheHit)
	assert.Len(t, resp.Messages, len(messages))

	// Count cache_control markers across all message parts
	markerCount := 0
	systemMarked := false
	for _, msg := range resp.Messages {
		for _, part := range msg.Parts {
			if cached, ok := part.(llms.CachedContent); ok {
				markerCount++
				if msg.Role == llms.ChatMessageTypeSystem {
					systemMarked = true
				}
				assert.Equal(t, "ephemeral", cached.CacheControl.Type)
			}
		}
	}

	assert.True(t, systemMarked, "System message must have a cache breakpoint")
	assert.True(t, markerCount >= 2 && markerCount <= 3, "Expected 2-3 breakpoints, got %d", markerCount)
}

func TestGetCacheTTL_PerModelAndScope(t *testing.T) {
	// 1. Static scopes (Global / Account) always return 12h
	assert.Equal(t, 12*time.Hour, getCacheTTL(CacheScopeGlobal, "gemini-2.5-flash"))
	assert.Equal(t, 12*time.Hour, getCacheTTL(CacheScopeAccount, "gemini-2.5-pro"))

	// 2. Conversation scope with Flash model -> 30m
	assert.Equal(t, 30*time.Minute, getCacheTTL(CacheScopeConversation, "gemini-2.5-flash"))
	assert.Equal(t, 30*time.Minute, getCacheTTL(CacheScopeConversation, "gemini-2.0-flash-lite"))

	// 3. Conversation scope with Pro model -> 10m
	assert.Equal(t, 10*time.Minute, getCacheTTL(CacheScopeConversation, "gemini-2.5-pro"))
	assert.Equal(t, 10*time.Minute, getCacheTTL(CacheScopeConversation, "gemini-1.5-pro"))

	// 4. Other models default to 10m
	assert.Equal(t, 10*time.Minute, getCacheTTL(CacheScopeConversation, "claude-3-7-sonnet"))
}

func TestGoogleAICacheProvider_ZeroLatencyHitPath(t *testing.T) {
	p := NewGoogleAICacheProvider()

	messages := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: "System prompt"}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "User question"}},
		},
	}

	cacheable, _ := identifyCacheableMessages(messages, CacheScopeAccount)
	cacheable = padMessagesIfRequired(cacheable, CacheScopeAccount)
	contentHash := hashContent(cacheable)
	credsFp := credsFingerprint("", "", "", "", "", "", "")
	cacheKey := generateCacheKey(CacheScopeAccount, "acc-hit-test", "", "test_agent", "gemini-2.5-flash", credsFp)

	// Seed cache in memory
	info := &CacheInfo{
		CacheName:      "cachedContents/existing-123",
		AccountId:      "acc-hit-test",
		AgentName:      "test_agent",
		Model:          "gemini-2.5-flash",
		ContentHash:    contentHash,
		CreatedAt:      time.Now().Add(-10 * time.Minute),
		ExpiresAt:      time.Now().Add(11 * time.Hour),
		ConversationId: "conv-1",
	}
	data, err := json.Marshal(info)
	assert.NoError(t, err)
	assert.NoError(t, common.CacheSet(p.namespace, cacheKey, data))

	req := &CacheRequest{
		Provider:       "googleai",
		Model:          "gemini-2.5-flash",
		AccountId:      "acc-hit-test",
		AgentName:      "test_agent",
		ConversationId: "conv-1",
		Messages:       messages,
		Scope:          CacheScopeAccount,
	}

	// ApplyCache should return HIT immediately without network calls
	resp := p.ApplyCache(context.Background(), req)
	assert.True(t, resp.CacheHit)
	assert.Len(t, resp.Options, 1)
}

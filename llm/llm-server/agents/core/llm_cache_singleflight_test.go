package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestGoogleAICacheProvider_ApplyCacheSingleflightsConcurrentCreates(t *testing.T) {
	namespace := fmt.Sprintf("test_googleai_cache_singleflight_%d", time.Now().UnixNano())
	common.CacheCreateNamespace(namespace, common.CacheNamespaceWithExpiration(time.Minute), common.CacheNamespaceWithMaxEntries(128))
	t.Cleanup(func() {
		require.NoError(t, common.CacheClear(namespace))
	})

	var createCalls atomic.Int32
	provider := &GoogleAICacheProvider{
		namespace: namespace,
		countTokensFunc: func(ctx context.Context, apiKey string, model string, messages []llms.MessageContent) (int32, error) {
			return 2_048, nil
		},
		verifyCacheExistsFunc: func(ctx context.Context, cacheName, apiKey string) bool {
			return cacheName != ""
		},
	}
	provider.createCacheFunc = func(ctx context.Context, req *CacheRequest, cacheableMessages []llms.MessageContent, contentHash, cacheKey string, tokenCount int32) (*CacheInfo, error) {
		createCalls.Add(1)
		time.Sleep(50 * time.Millisecond)

		createdAt := time.Now().UTC()
		cacheInfo := &CacheInfo{
			CacheName:           "cachedContents/shared-singleflight-cache",
			AccountId:           req.AccountId,
			ConversationId:      req.ConversationId,
			AgentName:           req.AgentName,
			Model:               req.Model,
			CreatedAt:           createdAt,
			ExpiresAt:           createdAt.Add(getCacheTTL(req.Scope)),
			ContentHash:         contentHash,
			CacheCreationTokens: tokenCount,
		}

		data, err := json.Marshal(cacheInfo)
		if err != nil {
			return nil, err
		}
		if err := common.CacheSet(namespace, cacheKey, data, common.CacheSetWithExpiration(getCacheTTL(req.Scope))); err != nil {
			return nil, err
		}
		return cacheInfo, nil
	}

	type cacheResult struct {
		activePrompt string
		response     *CacheResponse
	}

	const goroutines = 16
	start := make(chan struct{})
	results := make(chan cacheResult, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			activePrompt := fmt.Sprintf("debug cluster %d", i)
			results <- cacheResult{
				activePrompt: activePrompt,
				response: provider.ApplyCache(context.Background(), &CacheRequest{
					TenantId:       "tenant-1",
					AccountId:      "account-1",
					ConversationId: fmt.Sprintf("conversation-%d", i),
					AgentName:      "k8s_debug",
					Model:          "gemini-2.5-flash",
					Provider:       "googleai",
					Messages:       accountScopedMessages(strings.Repeat("stable account instructions ", 200), activePrompt),
					ApiKey:         "test-api-key",
					Scope:          CacheScopeAccount,
				}),
			}
		}(i)
	}

	close(start)
	wg.Wait()
	close(results)

	require.Equal(t, int32(1), createCalls.Load(), "concurrent calls for one account cache key must create one provider cache")
	creatorResponses := 0
	cacheHitResponses := 0
	for result := range results {
		resp := result.response
		require.NoError(t, resp.Error)
		require.Len(t, resp.Options, 1)

		opts := &llms.CallOptions{}
		resp.Options[0](opts)
		assert.Equal(t, "cachedContents/shared-singleflight-cache", opts.Metadata["CachedContentName"])
		assert.Len(t, resp.Messages, 1, "account scoped cache should leave only the active human prompt uncached")
		require.Len(t, resp.Messages[0].Parts, 1)
		textPart, ok := resp.Messages[0].Parts[0].(llms.TextContent)
		require.True(t, ok)
		assert.Equal(t, result.activePrompt, textPart.Text)

		if resp.CacheInfo != nil && resp.CacheInfo.CacheCreationTokens > 0 {
			assert.Equal(t, "cachedContents/shared-singleflight-cache", resp.CacheInfo.CacheName)
			creatorResponses++
			assert.False(t, resp.CacheHit)
		} else {
			if resp.CacheInfo != nil {
				assert.Equal(t, "cachedContents/shared-singleflight-cache", resp.CacheInfo.CacheName)
				assert.Zero(t, resp.CacheInfo.CacheCreationTokens)
			}
			cacheHitResponses++
			assert.True(t, resp.CacheHit)
		}
	}
	assert.Equal(t, 1, creatorResponses, "only the goroutine that executes createCache should report creation tokens")
	assert.Equal(t, goroutines-1, cacheHitResponses, "singleflight waiters should reuse the cache without reporting creation tokens")
}

func TestGoogleAICacheProvider_GetOrCreateCacheDoesNotSingleflightDifferentContent(t *testing.T) {
	namespace := fmt.Sprintf("test_googleai_cache_content_%d", time.Now().UnixNano())
	common.CacheCreateNamespace(namespace, common.CacheNamespaceWithExpiration(time.Minute), common.CacheNamespaceWithMaxEntries(128))
	t.Cleanup(func() {
		require.NoError(t, common.CacheClear(namespace))
	})

	var createCalls atomic.Int32
	leaderEntered := make(chan struct{})
	createEntered := make(chan struct{}, 2)
	releaseCreate := make(chan struct{})
	var closeLeaderEntered sync.Once
	var closeReleaseCreate sync.Once
	releaseBlockedCreates := func() {
		closeReleaseCreate.Do(func() {
			close(releaseCreate)
		})
	}
	t.Cleanup(releaseBlockedCreates)
	provider := &GoogleAICacheProvider{
		namespace: namespace,
		countTokensFunc: func(ctx context.Context, apiKey string, model string, messages []llms.MessageContent) (int32, error) {
			return 2_048, nil
		},
		verifyCacheExistsFunc: func(ctx context.Context, cacheName, apiKey string) bool {
			return cacheName != ""
		},
	}
	provider.createCacheFunc = func(ctx context.Context, req *CacheRequest, cacheableMessages []llms.MessageContent, contentHash, cacheKey string, tokenCount int32) (*CacheInfo, error) {
		createCalls.Add(1)
		createEntered <- struct{}{}
		closeLeaderEntered.Do(func() {
			close(leaderEntered)
		})
		<-releaseCreate

		createdAt := time.Now().UTC()
		cacheInfo := &CacheInfo{
			CacheName:           "cachedContents/" + contentHash,
			AccountId:           req.AccountId,
			ConversationId:      req.ConversationId,
			AgentName:           req.AgentName,
			Model:               req.Model,
			CreatedAt:           createdAt,
			ExpiresAt:           createdAt.Add(getCacheTTL(req.Scope)),
			ContentHash:         contentHash,
			CacheCreationTokens: tokenCount,
		}

		data, err := json.Marshal(cacheInfo)
		if err != nil {
			return nil, err
		}
		if err := common.CacheSet(namespace, cacheKey, data, common.CacheSetWithExpiration(getCacheTTL(req.Scope))); err != nil {
			return nil, err
		}
		return cacheInfo, nil
	}

	req := &CacheRequest{
		TenantId:       "tenant-1",
		AccountId:      "account-1",
		ConversationId: "conversation-1",
		AgentName:      "k8s_debug",
		Model:          "gemini-2.5-flash",
		Provider:       "googleai",
		ApiKey:         "test-api-key",
		Scope:          CacheScopeAccount,
	}
	cacheKey := generateCacheKey(req.Scope, req.AccountId, req.ConversationId, req.AgentName, req.Model, credsFingerprint(req.ApiKey, "", "", "", "", "", ""))

	type cacheInfoResult struct {
		cacheInfo *CacheInfo
		err       error
	}

	results := make(chan cacheInfoResult, 2)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cacheInfo, _, err := provider.getOrCreateCache(context.Background(), req, nil, "content-hash-a", cacheKey, 2_048)
		results <- cacheInfoResult{cacheInfo: cacheInfo, err: err}
	}()

	<-leaderEntered
	<-createEntered
	wg.Add(1)
	go func() {
		defer wg.Done()
		cacheInfo, _, err := provider.getOrCreateCache(context.Background(), req, nil, "content-hash-b", cacheKey, 2_048)
		results <- cacheInfoResult{cacheInfo: cacheInfo, err: err}
	}()

	select {
	case <-createEntered:
	case <-time.After(time.Second):
		t.Fatal("second content hash did not enter createCache while the first create was still blocked")
	}

	releaseBlockedCreates()
	wg.Wait()
	close(results)

	cacheNames := make(map[string]struct{})
	for result := range results {
		require.NoError(t, result.err)
		require.NotNil(t, result.cacheInfo)
		cacheNames[result.cacheInfo.CacheName] = struct{}{}
	}

	require.Equal(t, int32(2), createCalls.Load(), "different content hashes under the same cache slot must not share one provider cache")
	assert.Len(t, cacheNames, 2)
}

func accountScopedMessages(stablePrompt, activePrompt string) []llms.MessageContent {
	return []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: stablePrompt},
			},
		},
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: activePrompt},
			},
		},
	}
}

package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// TestGoogleAICacheProvider_GetOrCreateCache_DedupesConcurrentCreates is a
// regression test for the redundant Google AI cache creations under concurrent
// load. When many conversations race into ApplyCache for the same account-scoped
// cacheKey, exactly one cached-content resource must be created (the rest share
// the winner's result) — otherwise each redundant resource keeps billing storage
// for its full TTL.
//
// The provider is constructed directly with a counting createCacheFn stub, so
// the test exercises the singleflight de-duplication without touching the Google
// AI API.
func TestGoogleAICacheProvider_GetOrCreateCache_DedupesConcurrentCreates(t *testing.T) {
	var creates int32
	p := &GoogleAICacheProvider{namespace: "test-cache-ns"}
	p.createCacheFn = func(_ context.Context, _ *CacheRequest, _ []llms.MessageContent, contentHash, _ string, _ int32) (*CacheInfo, error) {
		atomic.AddInt32(&creates, 1)
		// Widen the window so the goroutines genuinely overlap inside Do.
		time.Sleep(25 * time.Millisecond)
		return &CacheInfo{
			CacheName:   "cachedContents/test",
			ContentHash: contentHash,
			ExpiresAt:   time.Now().Add(time.Hour),
		}, nil
	}

	const (
		goroutines = 32
		cacheKey   = "account:acc-1:k8s_debug:gemini-pro"
		contentH   = "content-hash-1"
	)

	req := &CacheRequest{}
	var wg sync.WaitGroup
	results := make([]*CacheInfo, goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = p.getOrCreateCache(context.Background(), req, nil, contentH, cacheKey, 23000)
		}(i)
	}
	wg.Wait()

	// Exactly one creation despite N concurrent callers for the same key.
	assert.Equal(t, int32(1), atomic.LoadInt32(&creates),
		"concurrent ApplyCache for the same cacheKey must create exactly one Google AI resource")

	// Every caller gets the same (valid) cache info.
	for i := 0; i < goroutines; i++ {
		require.NoError(t, errs[i])
		require.NotNil(t, results[i])
		assert.Equal(t, "cachedContents/test", results[i].CacheName)
	}
}

// TestGoogleAICacheProvider_GetOrCreateCache_DistinctKeys verifies the
// de-duplication is per cacheKey: different keys still create independently.
func TestGoogleAICacheProvider_GetOrCreateCache_DistinctKeys(t *testing.T) {
	var creates int32
	p := &GoogleAICacheProvider{namespace: "test-cache-ns"}
	p.createCacheFn = func(_ context.Context, _ *CacheRequest, _ []llms.MessageContent, contentHash, _ string, _ int32) (*CacheInfo, error) {
		atomic.AddInt32(&creates, 1)
		return &CacheInfo{CacheName: "cachedContents/test", ContentHash: contentHash, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}

	_, err1 := p.getOrCreateCache(context.Background(), &CacheRequest{}, nil, "h", "account:acc-1:k8s_debug:gemini-pro", 23000)
	_, err2 := p.getOrCreateCache(context.Background(), &CacheRequest{}, nil, "h", "account:acc-2:k8s_debug:gemini-pro", 23000)

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.Equal(t, int32(2), atomic.LoadInt32(&creates), "distinct cacheKeys must create independently")
}

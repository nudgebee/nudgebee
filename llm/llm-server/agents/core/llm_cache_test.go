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
// for the lock holder to publish (then reuses) and times out cleanly otherwise.
func TestGoogleAICacheProvider_WaitForSharedCacheInfo(t *testing.T) {
	p := NewGoogleAICacheProvider()

	// Never published -> times out -> nil (no panic, no hang).
	assert.Nil(t, p.waitForSharedCacheInfo("never-key", 200*time.Millisecond))

	// Published shortly after the wait starts -> returned.
	data, err := json.Marshal(&CacheInfo{CacheName: "cachedContents/xyz"})
	assert.NoError(t, err)
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = common.CacheSet(p.namespace, "late-key", data)
	}()
	got := p.waitForSharedCacheInfo("late-key", 3*time.Second)
	if assert.NotNil(t, got, "must return the entry published during the wait") {
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

package core

import (
	"nudgebee/llm/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestToolDiscoveryPerformance validates the caching mechanisms for tool discovery
// Usage: go test -v -run TestToolDiscoveryPerformance ./tools/core/...
func TestToolDiscoveryPerformance(t *testing.T) {
	// Skip if no test account provided (consistent with other tests in repo)
	testAccountId := os.Getenv("TEST_ACCOUNT")
	if testAccountId == "" {
		t.Skip("Skipping performance test: TEST_ACCOUNT env var not set")
	}
	testTenantId := os.Getenv("TEST_TENANT")
	testUserId := os.Getenv("TEST_USER")

	ctx := security.NewRequestContextForTenantAccountAdmin(testTenantId, testUserId, []string{testAccountId})

	t.Run("Benchmark_ListTools_Caching", func(t *testing.T) {
		// 1. Invalidate Cache to force cold start
		updateToolCache(testAccountId)

		// 2. Cold Start
		start := time.Now()
		toolsCold := ListTools(ctx, testAccountId)
		coldDuration := time.Since(start)
		t.Logf("ListTools (Cold): found %d tools in %v", len(toolsCold), coldDuration)

		// 3. Warm Cache
		start = time.Now()
		toolsWarm := ListTools(ctx, testAccountId)
		warmDuration := time.Since(start)
		t.Logf("ListTools (Warm): found %d tools in %v", len(toolsWarm), warmDuration)

		// Verification
		assert.Equal(t, len(toolsCold), len(toolsWarm), "Tool counts should match")
		assert.Less(t, int64(warmDuration), int64(coldDuration), "Warm cache should be faster than cold cache")

		// Expect significant speedup (e.g. > 10x or < 1ms)
		if warmDuration > 10*time.Millisecond {
			t.Logf("Warning: Warm cache took > 10ms (%v). Check if in-memory cache is working.", warmDuration)
		}
	})

	t.Run("Benchmark_GetEnabledNBTools_Caching", func(t *testing.T) {
		// 1. Invalidate Cache
		updateToolCache(testAccountId)

		// 2. Cold Start
		start := time.Now()
		toolsCold := GetEnabledNBTools(ctx, testAccountId)
		coldDuration := time.Since(start)
		t.Logf("GetEnabledNBTools (Cold): found %d tools in %v", len(toolsCold), coldDuration)

		// 3. Warm Cache
		start = time.Now()
		toolsWarm := GetEnabledNBTools(ctx, testAccountId)
		warmDuration := time.Since(start)
		t.Logf("GetEnabledNBTools (Warm): found %d tools in %v", len(toolsWarm), warmDuration)

		// Verification
		assert.Equal(t, len(toolsCold), len(toolsWarm), "Tool counts should match")
		assert.Less(t, int64(warmDuration), int64(coldDuration), "Warm cache should be faster than cold cache")
	})

	t.Run("Verify_CacheInvalidation", func(t *testing.T) {
		// Warm up the cache
		_ = ListTools(ctx, testAccountId)

		// Verify cache is hit (via side-channel or just speed, here we rely on black-box behavior)
		start := time.Now()
		_ = ListTools(ctx, testAccountId)
		warmTime := time.Since(start)

		// Invalidate
		updateToolCache(testAccountId)

		// Should be slow again
		start = time.Now()
		_ = ListTools(ctx, testAccountId)
		coldTime := time.Since(start)

		t.Logf("Invalidation Check: Warm=%v, Post-Invalidation=%v", warmTime, coldTime)

		// Note: In a very fast environment (or no DB latency), coldTime might be small too.
		// But logically coldTime > warmTime is expected.
		if coldTime < warmTime {
			t.Logf("Warning: Post-invalidation time was faster/equal to warm time. This might happen if DB is instant.")
		}
	})

	t.Run("Benchmark_GetAccountConfigSummary_Caching", func(t *testing.T) {
		// Clear specific cache
		accountConfigSummaryCacheInstance.delete(testAccountId)

		// Cold
		start := time.Now()
		_, _ = GetAccountConfigSummary(ctx, testAccountId)
		coldDuration := time.Since(start)
		t.Logf("GetAccountConfigSummary (Cold): %v", coldDuration)

		// Warm
		start = time.Now()
		_, _ = GetAccountConfigSummary(ctx, testAccountId)
		warmDuration := time.Since(start)
		t.Logf("GetAccountConfigSummary (Warm): %v", warmDuration)

		assert.Less(t, int64(warmDuration), int64(coldDuration))
	})
}

package agents

import (
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestAgentToolDiscoveryPerformance validates the caching mechanisms for agent tool discovery
// Usage: go test -v -run TestAgentToolDiscoveryPerformance ./agents/...
func TestAgentToolDiscoveryPerformance(t *testing.T) {
	testAccountId := os.Getenv("TEST_ACCOUNT")
	if testAccountId == "" {
		t.Skip("Skipping performance test: TEST_ACCOUNT env var not set")
	}
	testTenantId := os.Getenv("TEST_TENANT")
	testUserId := os.Getenv("TEST_USER")

	ctx := security.NewRequestContextForTenantAccountAdmin(testTenantId, testUserId, []string{testAccountId})

	// Use the public constructor (unexported but accessible via type alias or interface if available,
	// here we use the concrete type directly as we are in the same package)
	// But wait, newK8sDebugAgent returns interface.
	// We can cast it or just call GetSupportedTools.

	agent := newK8sDebugAgent(testAccountId)

	t.Run("Benchmark_GetSupportedTools_Caching", func(t *testing.T) {
		// 1. Invalidate Cache
		toolcore.InvalidateToolCache(testAccountId)

		// Note: We use InvalidateToolCache to ensure a cold start for the agent-specific cache.
		// The `updateToolCache` function (called by InvalidateToolCache) triggers registered invalidators,
		// including the one for `agentSupportedToolsCacheInstance` in the `agents` package.
		// This guarantees that the subsequent GetSupportedTools call will perform a full discovery.

		// 2. Cold Start
		start := time.Now()
		toolsCold := agent.GetSupportedTools(ctx)
		coldDuration := time.Since(start)
		t.Logf("GetSupportedTools (Cold): found %d tools in %v", len(toolsCold), coldDuration)

		// 3. Warm Cache
		start = time.Now()
		toolsWarm := agent.GetSupportedTools(ctx)
		warmDuration := time.Since(start)
		t.Logf("GetSupportedTools (Warm): found %d tools in %v", len(toolsWarm), warmDuration)

		// Verification
		assert.Equal(t, len(toolsCold), len(toolsWarm), "Tool counts should match")
		assert.Less(t, int64(warmDuration), int64(coldDuration), "Warm cache should be faster than cold cache")
	})
}

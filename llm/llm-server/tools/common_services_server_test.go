package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClampLogLimitForProvider pins the per-provider limit cap added 2026-06-22
// after a 30-day production sample showed 12 fetch_logs failures matching
// `loki: limit exceeds maximum of 5000`. The clamp lives in the tool layer so
// any caller (current agent prompt, a future integration, a raw API hit) gets
// a capped fetch instead of an opaque backend error.
func TestClampLogLimitForProvider(t *testing.T) {
	cases := []struct {
		name        string
		provider    string
		requested   int
		wantLimit   int
		wantClamped bool
	}{
		{
			name:        "loki at cap — no clamp, returns same",
			provider:    "loki",
			requested:   lokiMaxLogLimit,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: false,
		},
		{
			name:        "loki above cap — clamps to cap",
			provider:    "loki",
			requested:   10000,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "loki at 5001 — clamps to cap",
			provider:    "loki",
			requested:   lokiMaxLogLimit + 1,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "loki well below cap — no clamp",
			provider:    "loki",
			requested:   1000,
			wantLimit:   1000,
			wantClamped: false,
		},
		{
			name:        "case-insensitive provider match",
			provider:    "Loki",
			requested:   9999,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "whitespace-tolerant provider match (Gemini #32847 review)",
			provider:    "  loki  ",
			requested:   9999,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "unknown provider — passes through unchanged, no warning fires",
			provider:    "elasticsearch",
			requested:   10000,
			wantLimit:   10000,
			wantClamped: false,
		},
		{
			name:        "empty provider — passes through unchanged",
			provider:    "",
			requested:   10000,
			wantLimit:   10000,
			wantClamped: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, clamped := clampLogLimitForProvider(c.provider, c.requested)
			assert.Equal(t, c.wantLimit, got)
			assert.Equal(t, c.wantClamped, clamped)
		})
	}
}

// TestCloudProviderToObservabilityFallback pins the pure mapping used by the
// Get{Metrics,Log,Trace}Provider CLI fallback: only GCP/Azure cloud accounts
// route to a cloud-CLI observability backend; AWS and unknowns keep the
// existing prometheus/k8s/clickhouse defaults. Case and surrounding whitespace
// in cloud_accounts.cloud_provider must not defeat the match.
func TestCloudProviderToObservabilityFallback(t *testing.T) {
	cases := []struct {
		name          string
		cloudProvider string
		want          string
	}{
		{"gcp lowercase", "gcp", "gcp"},
		{"gcp uppercase", "GCP", "gcp"},
		{"gcp padded", "  gcp ", "gcp"},
		{"azure lowercase", "azure", "azure"},
		{"azure mixed case", "Azure", "azure"},
		{"aws lowercase", "aws", "aws"},
		{"aws uppercase", "AWS", "aws"},
		{"empty", "", ""},
		{"unknown", "digitalocean", ""},
		{"gke is not a cloud_provider value", "gke", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, cloudProviderToObservabilityFallback(c.cloudProvider))
		})
	}
}

// TestGetLogProvider_ServesCachedEntry asserts the cache-hit path short-circuits
// before the services-server round-trip. A single fetch_logs call resolves the
// log provider three times (LogAgent construction, FetchLogsAgent construction,
// logs_execute_v2 construction) and none of them share an instance, so this
// short-circuit is what keeps those three calls to one round-trip.
//
// The seeded provider is deliberately NOT "k8s": on a cache miss the uncached
// resolver returns the "k8s" default for a non-UUID account, so getting "loki"
// back is unambiguous proof the cached value was served.
func TestGetLogProvider_ServesCachedEntry(t *testing.T) {
	acct := "cached-provider-" + t.Name()
	seeded := services_server.ObservabilityProvider{
		Provider:          "loki",
		IntegrationSource: "integration",
		DefaultIndex:      "app-logs-*",
		Capabilities: services_server.ProviderCapabilities{
			SupportedOperators: []string{"_eq", "_ilike"},
			LabelMappings:      map[string]string{"app": "deployment.keyword", "namespace": "namespace"},
		},
	}

	data, err := common.MarshalJson(cachedLogProvider{Provider: seeded, ExpiresAt: time.Now().Add(logProviderCacheTTL)})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(logProviderCacheNS, acct, data, common.CacheSetWithExpiration(logProviderCacheTTL)))

	got, err := GetLogProvider(acct)
	require.NoError(t, err)
	assert.Equal(t, seeded.Provider, got.Provider)
	assert.Equal(t, seeded.IntegrationSource, got.IntegrationSource)
	assert.Equal(t, seeded.DefaultIndex, got.DefaultIndex)
	// Capabilities must survive the JSON round-trip: LabelMappings is what
	// buildCanonicalLogQueryPrompt renders as the canonical_name → backend_field
	// block. Losing it would silently drop the canonical vocabulary and push the
	// generator onto provider-native guesses, which match zero rows on backends
	// whose real keys differ (the exact failure the canonical path exists to fix).
	assert.Equal(t, seeded.Capabilities.LabelMappings, got.Capabilities.LabelMappings)
	assert.Equal(t, seeded.Capabilities.SupportedOperators, got.Capabilities.SupportedOperators)

	t.Run("invalidation drops the entry", func(t *testing.T) {
		require.NoError(t, common.CacheDelete(logProviderCacheNS, acct))
		_, ok := common.CacheGet(logProviderCacheNS, acct)
		assert.False(t, ok, "integration-change invalidation must drop the cached provider")
	})
}

// TestGetLogProvider_IgnoresExpiredEntry pins the stamped-expiry check. The
// default in_memory cache backend (bigcache) drops per-entry TTLs, so without
// this check a cached provider would live until the global LifeWindow evicted
// it — long past logProviderCacheTTL. The seeded entry is already expired, so
// serving it would be the bug; falling through to a fresh resolve (which yields
// the "k8s" default for this non-UUID account) is correct.
func TestGetLogProvider_IgnoresExpiredEntry(t *testing.T) {
	acct := "expired-provider-" + t.Name()
	stale := services_server.ObservabilityProvider{Provider: "loki"}

	data, err := common.MarshalJson(cachedLogProvider{Provider: stale, ExpiresAt: time.Now().Add(-time.Minute)})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(logProviderCacheNS, acct, data))

	got, err := GetLogProvider(acct)
	require.NoError(t, err)
	assert.NotEqual(t, "loki", got.Provider, "an expired entry must not be served")
}

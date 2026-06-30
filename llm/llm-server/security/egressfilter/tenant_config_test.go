package egressfilter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installFakeLoaderForTest registers a loader function and restores the
// prior loader on test cleanup. Tests asserting cache/Resolve behaviour
// install a fake; production-wired callers use InitTenantConfigLoader.
func installFakeLoaderForTest(t *testing.T, loader TenantConfigLoader) {
	t.Helper()
	prev := tenantConfigLoader
	tenantConfigLoader = loader
	t.Cleanup(func() {
		tenantConfigLoader = prev
		invalidateAllTenantConfigsForTest()
	})
	invalidateAllTenantConfigsForTest()
}

func TestResolve_NoTenantInContext(t *testing.T) {
	installFakeLoaderForTest(t, func(_ context.Context, _ uuid.UUID) (*TenantConfig, error) {
		t.Fatal("loader must not be called when no tenant id is attached")
		return nil, nil
	})
	assert.Nil(t, Resolve(context.Background()))
}

func TestResolve_NoLoaderRegistered(t *testing.T) {
	prev := tenantConfigLoader
	tenantConfigLoader = nil
	t.Cleanup(func() { tenantConfigLoader = prev })

	ctx := WithTenantID(context.Background(), uuid.New())
	assert.Nil(t, Resolve(ctx))
}

func TestResolve_CacheHitDoesNotRefetch(t *testing.T) {
	var calls int64
	tenantID := uuid.New()
	cfg := &TenantConfig{TenantID: tenantID, Mode: ModeEnforce, Enabled: true}

	installFakeLoaderForTest(t, func(_ context.Context, _ uuid.UUID) (*TenantConfig, error) {
		atomic.AddInt64(&calls, 1)
		return cfg, nil
	})

	ctx := WithTenantID(context.Background(), tenantID)
	first := Resolve(ctx)
	second := Resolve(ctx)
	third := Resolve(ctx)

	require.NotNil(t, first)
	assert.Same(t, first, second)
	assert.Same(t, second, third)
	assert.Equal(t, int64(1), atomic.LoadInt64(&calls), "loader must be hit exactly once across N cached reads")
}

func TestResolve_NegativeCacheRememberedAsNil(t *testing.T) {
	var calls int64
	installFakeLoaderForTest(t, func(_ context.Context, _ uuid.UUID) (*TenantConfig, error) {
		atomic.AddInt64(&calls, 1)
		return nil, nil // no override row for this tenant
	})

	ctx := WithTenantID(context.Background(), uuid.New())
	assert.Nil(t, Resolve(ctx))
	assert.Nil(t, Resolve(ctx))
	assert.Nil(t, Resolve(ctx))
	assert.Equal(t, int64(1), atomic.LoadInt64(&calls),
		"loader returning (nil,nil) should also be cached so absent rows don't re-query")
}

func TestResolve_DBErrorFailsOpen(t *testing.T) {
	installFakeLoaderForTest(t, func(_ context.Context, _ uuid.UUID) (*TenantConfig, error) {
		return nil, errors.New("simulated DB outage")
	})
	ctx := WithTenantID(context.Background(), uuid.New())
	assert.Nil(t, Resolve(ctx), "DB error must fail-open to nil (caller uses env defaults)")
}

// TestResolve_DBErrorNegativeCached pins the contract that an upstream
// loader error is cached for the short error-TTL so a DB outage doesn't
// cause every LLM call to re-hit the failing DB. Without this, the cache
// layer would amplify the outage instead of dampening it.
func TestResolve_DBErrorNegativeCached(t *testing.T) {
	var calls int64
	installFakeLoaderForTest(t, func(_ context.Context, _ uuid.UUID) (*TenantConfig, error) {
		atomic.AddInt64(&calls, 1)
		return nil, errors.New("simulated DB outage")
	})

	ctx := WithTenantID(context.Background(), uuid.New())
	assert.Nil(t, Resolve(ctx)) // load #1 — errors
	assert.Nil(t, Resolve(ctx)) // expected cache hit — must NOT re-query
	assert.Nil(t, Resolve(ctx))
	assert.Equal(t, int64(1), atomic.LoadInt64(&calls),
		"loader must be called once; subsequent calls must hit the negative cache")
}

// TestResolve_CtxCancelErrorNotCached pins the contract that request-
// scoped ctx cancellation / timeout errors are NOT negative-cached.
// Otherwise a single timed-out request would temporarily disable per-
// tenant config for every other healthy concurrent request for the same
// tenant. Regression for the second Gemini review on PR #33303.
func TestResolve_CtxCancelErrorNotCached(t *testing.T) {
	var calls int64
	installFakeLoaderForTest(t, func(ctx context.Context, _ uuid.UUID) (*TenantConfig, error) {
		atomic.AddInt64(&calls, 1)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &TenantConfig{Enabled: true}, nil
	})

	tenantID := uuid.New()

	// First request: cancelled ctx → loader errors.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Nil(t, Resolve(WithTenantID(cancelled, tenantID)))
	assert.Equal(t, int64(1), atomic.LoadInt64(&calls))

	// Second request, same tenant, healthy ctx: must re-query the loader
	// (the negative cache was suppressed for the cancelled case).
	cfg := Resolve(WithTenantID(context.Background(), tenantID))
	assert.NotNil(t, cfg, "healthy ctx must successfully load after a cancelled-ctx error")
	assert.Equal(t, int64(2), atomic.LoadInt64(&calls),
		"cancelled-ctx error must NOT have been cached")
}

// TestTenantConfigCacheReapExpired locks the cleanup behaviour: entries
// whose expiresAt is in the past must be deleted; valid entries must
// remain. Without this, the cache grows unbounded by one entry per
// tenant ever resolved.
func TestTenantConfigCacheReapExpired(t *testing.T) {
	invalidateAllTenantConfigsForTest()
	t.Cleanup(invalidateAllTenantConfigsForTest)

	fresh := uuid.New()
	stale1 := uuid.New()
	stale2 := uuid.New()

	tenantCacheMu.Lock()
	tenantCacheEntries[fresh] = &tenantCacheEntry{
		cfg:       &TenantConfig{TenantID: fresh},
		expiresAt: time.Now().Add(1 * time.Minute),
	}
	tenantCacheEntries[stale1] = &tenantCacheEntry{
		cfg:       nil,
		expiresAt: time.Now().Add(-1 * time.Second),
	}
	tenantCacheEntries[stale2] = &tenantCacheEntry{
		cfg:       &TenantConfig{TenantID: stale2},
		expiresAt: time.Now().Add(-1 * time.Hour),
	}
	tenantCacheMu.Unlock()

	tenantConfigCacheReapExpired()

	tenantCacheMu.RLock()
	defer tenantCacheMu.RUnlock()
	_, freshExists := tenantCacheEntries[fresh]
	_, stale1Exists := tenantCacheEntries[stale1]
	_, stale2Exists := tenantCacheEntries[stale2]
	assert.True(t, freshExists, "fresh entry must be retained")
	assert.False(t, stale1Exists, "expired entry (stale1) must be deleted")
	assert.False(t, stale2Exists, "expired entry (stale2) must be deleted")
}

func TestResolve_TTLExpiryRefetches(t *testing.T) {
	prevTTL := tenantConfigTTL
	SetTenantConfigTTL(50 * time.Millisecond)
	t.Cleanup(func() { SetTenantConfigTTL(prevTTL) })

	var calls int64
	tenantID := uuid.New()
	installFakeLoaderForTest(t, func(_ context.Context, _ uuid.UUID) (*TenantConfig, error) {
		atomic.AddInt64(&calls, 1)
		return &TenantConfig{TenantID: tenantID, Mode: ModeDetect, Enabled: true}, nil
	})

	ctx := WithTenantID(context.Background(), tenantID)
	Resolve(ctx) // load #1
	Resolve(ctx) // cache hit
	time.Sleep(80 * time.Millisecond)
	Resolve(ctx) // expired → load #2

	assert.Equal(t, int64(2), atomic.LoadInt64(&calls))
}

func TestInvalidateTenantConfig_ForcesRefetch(t *testing.T) {
	var calls int64
	tenantID := uuid.New()
	installFakeLoaderForTest(t, func(_ context.Context, _ uuid.UUID) (*TenantConfig, error) {
		atomic.AddInt64(&calls, 1)
		return &TenantConfig{TenantID: tenantID, Mode: ModeDetect, Enabled: true}, nil
	})

	ctx := WithTenantID(context.Background(), tenantID)
	Resolve(ctx) // load #1
	Resolve(ctx) // cache hit
	InvalidateTenantConfig(tenantID)
	Resolve(ctx) // forced reload → load #2

	assert.Equal(t, int64(2), atomic.LoadInt64(&calls))
}

// applyTenantOverrides — pure-function tests, no cache or loader needed.

func TestApplyTenantOverrides_NilCfgIsNoop(t *testing.T) {
	hits := []Hit{{RuleID: "x", Start: 0, End: 3}}
	out := applyTenantOverrides(Result{Hits: hits}, "abc", nil)
	assert.Equal(t, hits, out.Hits)
}

func TestApplyTenantOverrides_AllowlistDropsMatchingHits(t *testing.T) {
	text := "value=AKIAIOSFODNN7EXAMPLE other=AKIAREALPRODKEY12345"
	r := Result{Hits: []Hit{
		{RuleID: "aws-access-key-id", Start: 6, End: 26},  // AKIAIOSFODNN7EXAMPLE
		{RuleID: "aws-access-key-id", Start: 33, End: 53}, // AKIAREALPRODKEY12345
	}}
	tcfg := &TenantConfig{Allowlist: []string{"AKIAIOSFODNN7EXAMPLE"}}
	out := applyTenantOverrides(r, text, tcfg)
	require.Len(t, out.Hits, 1)
	assert.Equal(t, 33, out.Hits[0].Start)
}

func TestApplyTenantOverrides_DisabledRulesDropsAllMatching(t *testing.T) {
	r := Result{Hits: []Hit{
		{RuleID: "aws-access-key-id", Start: 0, End: 5},
		{RuleID: "openai-api-key", Start: 10, End: 20},
	}}
	tcfg := &TenantConfig{DisabledRules: []string{"aws-access-key-id"}}
	out := applyTenantOverrides(r, "anytext-doesnt-matter", tcfg)
	require.Len(t, out.Hits, 1)
	assert.Equal(t, "openai-api-key", out.Hits[0].RuleID)
}

func TestApplyTenantOverrides_FailsClosedOnBadOffsets(t *testing.T) {
	r := Result{Hits: []Hit{
		{RuleID: "bad", Start: -1, End: 3},
		{RuleID: "bad", Start: 5, End: 2},
		{RuleID: "bad", Start: 0, End: 999},
	}}
	tcfg := &TenantConfig{Allowlist: []string{"anything"}}
	out := applyTenantOverrides(r, "short", tcfg)
	assert.Len(t, out.Hits, 3, "malformed offsets must keep the hit (fail-closed)")
}

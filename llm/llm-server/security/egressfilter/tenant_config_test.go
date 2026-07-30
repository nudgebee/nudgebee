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

// --- PII effective-value helpers (V827) --------------------------------------
//
// The wrapper reads env baselines and per-tenant overrides through these
// helpers; the merging rule (nil / empty = inherit env; non-nil / non-empty =
// explicit override) is the tri-state semantics locked in by the DB schema
// (all four columns nullable-or-empty-default).

func ptrBool(v bool) *bool { return &v }

func TestEffectivePIIEnabled(t *testing.T) {
	// 2026-07-30 semantics: per-tenant opt-in. Only an explicit TRUE turns
	// PII on. Nil / missing / explicit false all mean off. Env doesn't
	// factor into this helper anymore (env is a hard gate at wrapper
	// install time; if the wrapper is running, this call decides).

	// Nil receiver (no row / no tenant) → off.
	var nilCfg *TenantConfig
	assert.False(t, nilCfg.EffectivePIIEnabled())

	// Tenant row exists but PIIEnabled is nil (tenant hasn't opted in) → off.
	tcfg := &TenantConfig{}
	assert.False(t, tcfg.EffectivePIIEnabled(), "tenant that hasn't touched PII stays off")

	// Explicit true → on.
	tcfg.PIIEnabled = ptrBool(true)
	assert.True(t, tcfg.EffectivePIIEnabled())

	// Explicit false → off.
	tcfg.PIIEnabled = ptrBool(false)
	assert.False(t, tcfg.EffectivePIIEnabled())
}

func TestEffectivePIIMode(t *testing.T) {
	var nilCfg *TenantConfig
	// Empty env falls back to "detect" so ops don't need to worry about
	// missing env producing an unknown mode.
	assert.Equal(t, "detect", nilCfg.EffectivePIIMode(""))
	assert.Equal(t, "enforce", nilCfg.EffectivePIIMode("enforce"))

	tcfg := &TenantConfig{}
	assert.Equal(t, "enforce", tcfg.EffectivePIIMode("enforce"))
	tcfg.PIIMode = "detect"
	assert.Equal(t, "detect", tcfg.EffectivePIIMode("enforce"), "tenant explicit detect must override env enforce")
	tcfg.PIIMode = "enforce"
	assert.Equal(t, "enforce", tcfg.EffectivePIIMode("detect"))
}

func TestIsPIICategoryDisabled(t *testing.T) {
	var nilCfg *TenantConfig
	assert.False(t, nilCfg.IsPIICategoryDisabled("EMAIL"))

	tcfg := &TenantConfig{PIIDisabledCategories: []string{"EMAIL", "PHONE"}}
	assert.True(t, tcfg.IsPIICategoryDisabled("EMAIL"))
	assert.True(t, tcfg.IsPIICategoryDisabled("email"), "case-insensitive")
	assert.True(t, tcfg.IsPIICategoryDisabled("  Phone  "), "trims whitespace")
	assert.False(t, tcfg.IsPIICategoryDisabled("PERSON"))
	assert.False(t, tcfg.IsPIICategoryDisabled(""))
}

// FilterPIIMappingByCategory: the routing rule underneath the wrapper's
// per-tenant category filter. Kept + unscrub sets partition the input
// mapping so downstream code can rehydrate disabled-category tokens back
// to real values BEFORE sending to the LLM.
func TestFilterPIIMappingByCategory(t *testing.T) {
	mapping := map[string]string{
		"[EMAIL_1]":  "alice@acme.co",
		"[EMAIL_2]":  "bob@acme.co",
		"[PERSON_1]": "Alice Doe",
		"[PHONE_1]":  "+1-555-0100",
	}

	t.Run("empty disabled set returns mapping unchanged", func(t *testing.T) {
		kept, unscrub := FilterPIIMappingByCategory(mapping, nil)
		assert.Equal(t, mapping, kept)
		assert.Nil(t, unscrub)
	})

	t.Run("splits by category (case-insensitive)", func(t *testing.T) {
		kept, unscrub := FilterPIIMappingByCategory(mapping, []string{"email"})
		assert.Len(t, kept, 2)
		assert.Contains(t, kept, "[PERSON_1]")
		assert.Contains(t, kept, "[PHONE_1]")
		assert.Len(t, unscrub, 2)
		assert.Equal(t, "alice@acme.co", unscrub["[EMAIL_1]"])
		assert.Equal(t, "bob@acme.co", unscrub["[EMAIL_2]"])
	})

	t.Run("all categories disabled empties kept", func(t *testing.T) {
		kept, unscrub := FilterPIIMappingByCategory(mapping, []string{"EMAIL", "PERSON", "PHONE"})
		assert.Empty(t, kept)
		assert.Len(t, unscrub, 4)
	})

	t.Run("unknown category is a no-op", func(t *testing.T) {
		// Admin API rejects unknown categories at write time, but the
		// helper must still behave sanely if the wrapper is somehow passed
		// one (defensive).
		kept, unscrub := FilterPIIMappingByCategory(mapping, []string{"NONSENSE"})
		assert.Len(t, kept, 4)
		assert.Empty(t, unscrub)
	})
}

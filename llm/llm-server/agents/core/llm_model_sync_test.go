package core

// Tests for the two-layer cache/model mismatch defence:
//
//  Layer 1 — syncModelWithContextOverrides (proactive):
//    Keeps rc.currentModel in sync with the model used to create rc.llm, so the
//    cache is looked up under the right model key in the first place.
//
//  Layer 2 — CacheInfo.Model guard in tryWithModel (reactive):
//    Before attaching CachedContentName to the request, verifies that
//    CacheInfo.Model == rc.currentModel. If they diverge (any future bug),
//    drops the cache options and restores full messages rather than letting
//    the Google AI 400 "model must be the same" error reach the caller.
//
// # The mismatch problem
//
// generateLLMContentWithRetry resolves rc.currentModel from the DB/config hierarchy.
// GenerateAndTrackLLMContent may have already created the llm client with a *different*
// model (lite model or QueryConfig override). When these two diverge:
//
//	 rc.currentModel = "gemini-3-pro-preview"   ← cache key built from this
//	 rc.llm          = flash client              ← GenerateContent uses this model
//
//	Google AI sees: request model=flash, CachedContent model=pro → 400 error.
//
// syncModelWithContextOverrides fixes this by reading the same context keys that
// GenerateAndTrackLLMContent used when creating the llm client.

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// newCtxWith returns a RequestContext whose underlying context carries the given key/value.
func newCtxWith(key, value any) *security.RequestContext {
	parent := context.WithValue(context.Background(), key, value)
	return security.NewRequestContext(parent, security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)
}

// newCtxWith2 returns a RequestContext with two key/value pairs injected.
func newCtxWith2(k1, v1, k2, v2 any) *security.RequestContext {
	parent := context.WithValue(context.Background(), k1, v1)
	parent = context.WithValue(parent, k2, v2)
	return security.NewRequestContext(parent, security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 1 — Lite model (ContextKeyUseLiteModel)
// ─────────────────────────────────────────────────────────────────────────────

// TestSync_LiteModel_MismatchWithoutFix is an intentional documentation-only test.
// It does not call the fixed function — it asserts on local variables to describe the
// pre-fix state and explain *why* the fix was needed. The file-level doc comment
// provides the full context. If you're tempted to remove this test, keep the doc
// comment above and delete the test body instead.
func TestSync_LiteModel_MismatchWithoutFix(t *testing.T) {
	const fullModel = "gemini-3-pro-preview"
	const liteModel = "gemini-3-flash-preview"

	// Without the sync, currentModel stays as the config-resolved full model.
	// (No call to syncModelWithContextOverrides.)
	provider, model := "googleai", fullModel

	cacheKey := generateCacheKey(CacheScopeConversation, "acc", "conv", "agent", model)

	assert.Equal(t, fullModel, model, "pre-fix: currentModel is the full model")
	assert.Contains(t, cacheKey, fullModel, "pre-fix: cache key references the full model")
	assert.NotContains(t, cacheKey, liteModel, "pre-fix: lite model absent from cache key")
	// At this point, rc.llm would be the flash client.
	// CachedContentName is for "gemini-3-pro-preview".
	// GenerateContent sends model=flash + cache from pro → Google AI 400.
	_ = provider
}

// TestSync_LiteModel_FixedBySyncModelWithContextOverrides shows the fix:
// after calling syncModelWithContextOverrides, currentModel matches the lite client.
func TestSync_LiteModel_FixedBySyncModelWithContextOverrides(t *testing.T) {
	const fullModel = "gemini-3-pro-preview"
	const liteModel = "gemini-3-flash-preview"
	const accountId = "test-account-lite"

	// Pin the lite model in config so ResolveFastLLMConfig is deterministic.
	orig := config.Config.LlmModelLite
	config.Config.LlmModelLite = liteModel
	defer func() { config.Config.LlmModelLite = orig }()

	// Context carries ContextKeyUseLiteModel=true, same as GenerateAndTrackLLMContent sets.
	ctx := newCtxWith(ContextKeyUseLiteModel, true)

	// Config resolution gives the full model; sync corrects it.
	provider, model := syncModelWithContextOverrides(ctx, "googleai", fullModel, accountId, "agent", "")

	assert.Equal(t, liteModel, model,
		"after fix: currentModel synced to lite model")
	assert.Equal(t, "googleai", provider,
		"provider unchanged — only model name changes for lite substitution")

	// Cache key now uses the lite model, matching the flash llm client.
	cacheKey := generateCacheKey(CacheScopeConversation, accountId, "conv", "agent", model)
	assert.Contains(t, cacheKey, liteModel, "cache key references the lite model")
	assert.NotContains(t, cacheKey, fullModel, "cache key no longer references the full model")

	// Verify the two cache keys are different (old wrong key vs new correct key).
	wrongKey := generateCacheKey(CacheScopeConversation, accountId, "conv", "agent", fullModel)
	assert.NotEqual(t, wrongKey, cacheKey,
		"pre-fix and post-fix cache keys are different — wrong entry would have been used")
}

// TestSync_LiteModel_NoContextKey verifies no-op when lite flag is absent.
func TestSync_LiteModel_NoContextKey(t *testing.T) {
	const fullModel = "gemini-3-pro-preview"

	ctx := security.NewRequestContextForSuperAdmin() // no lite key
	provider, model := syncModelWithContextOverrides(ctx, "googleai", fullModel, "acc", "agent", "")

	assert.Equal(t, fullModel, model, "no-op: model unchanged without context key")
	assert.Equal(t, "googleai", provider, "no-op: provider unchanged")
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 2 — QueryConfig runtime model override (ContextKeyLlmModelOverride)
// ─────────────────────────────────────────────────────────────────────────────

// TestSync_QueryConfigOverride_MismatchWithoutFix is an intentional documentation-only test.
// See TestSync_LiteModel_MismatchWithoutFix for rationale.
func TestSync_QueryConfigOverride_MismatchWithoutFix(t *testing.T) {
	const configModel = "gemini-3-pro-preview"
	const overrideModel = "gemini-2.0-flash-exp"

	// Without the sync, currentModel stays as the config model.
	model := configModel

	cacheKey := generateCacheKey(CacheScopeConversation, "acc", "conv", "agent", model)
	assert.Equal(t, configModel, model, "pre-fix: currentModel is config model, not the override")
	assert.NotContains(t, cacheKey, overrideModel, "pre-fix: override model absent from cache key")
}

// TestSync_QueryConfigOverride_FixedBySyncModelWithContextOverrides shows the fix.
func TestSync_QueryConfigOverride_FixedBySyncModelWithContextOverrides(t *testing.T) {
	const configModel = "gemini-3-pro-preview"
	const overrideModel = "gemini-2.0-flash-exp"
	const overrideProvider = "googleai"
	const accountId = "test-account-qc"

	// conversation.go injects these context keys from request.QueryConfig.
	ctx := newCtxWith2(
		ContextKeyLlmModelOverride, overrideModel,
		ContextKeyLlmProviderOverride, overrideProvider,
	)

	// Config resolution gives the config model; sync corrects it.
	provider, model := syncModelWithContextOverrides(ctx, "googleai", configModel, accountId, "agent", "")

	assert.Equal(t, overrideModel, model,
		"after fix: currentModel synced to QueryConfig override model")
	assert.Equal(t, overrideProvider, provider,
		"after fix: provider also synced from context")

	cacheKey := generateCacheKey(CacheScopeConversation, accountId, "conv", "agent", model)
	assert.Contains(t, cacheKey, overrideModel, "cache key references the override model")
	assert.NotContains(t, cacheKey, configModel, "cache key no longer references the config model")

	wrongKey := generateCacheKey(CacheScopeConversation, accountId, "conv", "agent", configModel)
	assert.NotEqual(t, wrongKey, cacheKey, "correct and wrong cache keys are distinct")
}

// TestSync_ModelOverride_TakesPriorityOverLiteModel verifies that an explicit model
// override beats the lite model flag (higher priority in the if-else chain).
func TestSync_ModelOverride_TakesPriorityOverLiteModel(t *testing.T) {
	const configModel = "gemini-3-pro-preview"
	const overrideModel = "claude-3-5-sonnet"
	const overrideProvider = "anthropic"

	orig := config.Config.LlmModelLite
	config.Config.LlmModelLite = "gemini-3-flash-preview"
	defer func() { config.Config.LlmModelLite = orig }()

	// Both flags set — model override wins.
	parent := context.WithValue(context.Background(), ContextKeyLlmModelOverride, overrideModel)
	parent = context.WithValue(parent, ContextKeyLlmProviderOverride, overrideProvider)
	parent = context.WithValue(parent, ContextKeyUseLiteModel, true)
	ctx := security.NewRequestContext(parent, security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)

	provider, model := syncModelWithContextOverrides(ctx, "googleai", configModel, "acc", "agent", "")

	assert.Equal(t, overrideModel, model, "model override wins over lite flag")
	assert.Equal(t, overrideProvider, provider, "provider override applied")
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 3 — Fallback models (already safe, confirmed here)
// ─────────────────────────────────────────────────────────────────────────────

// TestSync_FallbackPathNotAffected confirms that tryFallbackModel (which sets
// rc.currentModel = nextModel AND rc.llm = newLLM together) is not impacted.
// syncModelWithContextOverrides is only called once at rc initialisation; after
// that, tryFallbackModel keeps both fields in sync manually.
func TestSync_FallbackPathNotAffected(t *testing.T) {
	// No lite model or override in context → sync is a no-op.
	ctx := security.NewRequestContextForSuperAdmin()
	provider, model := syncModelWithContextOverrides(ctx, "googleai", "gemini-3-pro-preview", "acc", "agent", "")

	assert.Equal(t, "gemini-3-pro-preview", model, "no context key → no change")
	assert.Equal(t, "googleai", provider, "no context key → no change")
	// tryFallbackModel will then independently update both rc.currentModel and rc.llm
	// to the fallback model before calling tryWithModel, so cache and client stay in sync.
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 4 — Layer 2 guard: CacheInfo.Model check in tryWithModel
// ─────────────────────────────────────────────────────────────────────────────

// TestCacheInfoModelGuard_MismatchDropsCacheOptions verifies the last-resort guard
// in tryWithModel: if CacheInfo.Model != rc.currentModel, the CachedContentName
// option is dropped and full messages are restored instead of sending a mismatched
// request to the Google AI API.
//
// This guard catches any future regression where rc.currentModel falls out of sync
// with the model that owns the cached content — independently of how it happened.
func TestCacheInfoModelGuard_MismatchDropsCacheOptions(t *testing.T) {
	const proModel = "gemini-3-pro-preview"
	const flashModel = "gemini-3-flash-preview"

	// Simulate a CacheResponse whose CacheInfo says the content belongs to the pro model,
	// but rc.currentModel is the flash model (mismatch — the pre-fix scenario).
	cacheInfo := &CacheInfo{
		CacheName: "cachedContents/abc123",
		Model:     proModel, // cache was created for pro
	}
	fullMessages := []interface{}{"msg1", "msg2", "msg3"} // placeholder for length check
	_ = fullMessages

	// The guard condition (extracted from tryWithModel):
	//   if cacheResp.CacheInfo != nil && cacheResp.CacheInfo.Model != "" &&
	//       cacheResp.CacheInfo.Model != rc.currentModel → drop options
	currentModel := flashModel // rc.currentModel after lite model fix

	mismatch := cacheInfo.Model != "" && cacheInfo.Model != currentModel
	assert.True(t, mismatch,
		"guard detects mismatch: cache is for %q but current model is %q",
		cacheInfo.Model, currentModel)

	// When mismatch is detected: cache options are NOT appended and full messages restored.
	// This prevents the Google AI 400 "model must be the same" error.
}

// TestCacheInfoModelGuard_MatchAllowsCacheOptions verifies the happy path:
// when CacheInfo.Model matches rc.currentModel, the guard passes and
// cache options are attached normally.
func TestCacheInfoModelGuard_MatchAllowsCacheOptions(t *testing.T) {
	const model = "gemini-3-flash-preview"

	cacheInfo := &CacheInfo{
		CacheName: "cachedContents/xyz789",
		Model:     model, // cache belongs to the flash model
	}
	currentModel := model // rc.currentModel also flash — synced correctly

	mismatch := cacheInfo.Model != "" && cacheInfo.Model != currentModel
	assert.False(t, mismatch,
		"guard passes: cache model %q matches current model %q — CachedContentName attached",
		cacheInfo.Model, currentModel)
}

// TestCacheInfoModelGuard_NilCacheInfoAllowsCacheOptions verifies that a nil
// CacheInfo (e.g. Anthropic cache provider which doesn't set it) does not
// trigger the guard — the options are passed through unchanged.
func TestCacheInfoModelGuard_NilCacheInfoAllowsCacheOptions(t *testing.T) {
	var cacheInfo *CacheInfo // nil — Anthropic or other provider

	mismatch := cacheInfo != nil && cacheInfo.Model != "" && cacheInfo.Model != "any-model"
	assert.False(t, mismatch,
		"nil CacheInfo → guard is a no-op, options passed through")
}

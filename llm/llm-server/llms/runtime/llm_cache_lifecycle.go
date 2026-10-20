package runtime

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// CacheLifecycleRecord describes one provider-side prompt cache resource for
// the llm_cache_lifecycle table. The table records physical facts only —
// storage cost is computed on read via JOIN to llm_model_pricing.
type CacheLifecycleRecord struct {
	CacheName      string // provider's cache resource ID, e.g. "cachedContents/abc123"
	LLMProvider    string // 'googleai' / 'anthropic' / etc
	LLMModel       string
	Scope          string  // 'global' | 'tenant' | 'account' | 'conversation'
	TenantID       *string // nullable — set for tenant/account/conversation
	AccountID      *string // nullable — set for account/conversation
	ConversationID *string // nullable — set only for conversation scope
	AgentName      *string // nullable
	CachedTokens   int64
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// cacheLifecycleWriteTimeout caps each lifecycle INSERT/UPDATE so a stalled DB
// connection can't pin the LLM call path. Lifecycle writes happen inline (not
// in a goroutine) because: (a) cache-create events are rare so the latency cost
// is negligible, and (b) async writes were silently lost when the goroutine
// didn't get to run before pod shutdown / DB blip — leaving provider-side cache
// resources alive but unrecorded for the rest of their TTL.
const cacheLifecycleWriteTimeout = 5 * time.Second

// recordCacheLifecycle records a new cache inline with the calling LLM path.
// Failures are logged at Error level (visible in default Sentry/Loki severity
// filters) but never propagated — recording cost should never fail the LLM
// call. A short context timeout bounds DB hangs.
//
// Synchronous-not-async: see cacheLifecycleWriteTimeout doc — async lost rows
// when goroutines didn't get to run before pod shutdown.
func recordCacheLifecycle(record *CacheLifecycleRecord) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cache lifecycle insert: panic recovered", "panic", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), cacheLifecycleWriteTimeout)
	defer cancel()
	if err := getStore().InsertCacheLifecycle(ctx, record); err != nil {
		slog.Error("cache lifecycle insert failed (cost will undercount this cache)",
			"error", err,
			"cache_name", record.CacheName,
			"scope", record.Scope)
	}
}

// recordCacheLifecycleInvalidation marks the cache invalidated inline.
// Same failure semantics as recordCacheLifecycle.
func recordCacheLifecycleInvalidation(cacheName string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cache lifecycle invalidation: panic recovered", "panic", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), cacheLifecycleWriteTimeout)
	defer cancel()
	if err := getStore().SetCacheLifecycleInvalidated(ctx, cacheName); err != nil {
		slog.Error("cache lifecycle invalidation update failed",
			"error", err,
			"cache_name", cacheName)
	}
}

// stringPtrIfNotEmpty is a helper for callers building CacheLifecycleRecord.
// Returns nil if the input is empty; otherwise a pointer to the string.
func stringPtrIfNotEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

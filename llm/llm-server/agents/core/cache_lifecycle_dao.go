package core

import (
	"context"
	"database/sql"
	"fmt"
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

// InsertCacheLifecycle records a new cache resource. Idempotent — duplicate
// inserts (same cache_name) are silently ignored to handle the rare race
// where two requests trigger createCache concurrently and the provider
// deduplicates by content hash.
func (chat *ConversationDao) InsertCacheLifecycle(ctx context.Context, record *CacheLifecycleRecord) error {
	if record == nil {
		return fmt.Errorf("cache lifecycle record is nil")
	}
	if strings.TrimSpace(record.CacheName) == "" {
		return fmt.Errorf("cache_name is required")
	}

	query := `
		INSERT INTO llm_cache_lifecycle (
			cache_name, llm_provider, llm_model, scope,
			tenant_id, account_id, conversation_id, agent_name,
			cached_tokens, created_at, expires_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11
		)
		ON CONFLICT (cache_name) DO NOTHING
	`

	_, err := chat.dbManager.Db.ExecContext(ctx, query,
		record.CacheName, record.LLMProvider, record.LLMModel, record.Scope,
		toUUIDOrNil(record.TenantID), toUUIDOrNil(record.AccountID), toUUIDOrNil(record.ConversationID), toNullableText(record.AgentName),
		record.CachedTokens, record.CreatedAt, record.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("InsertCacheLifecycle: %w", err)
	}
	return nil
}

// SetCacheLifecycleInvalidated marks a cache as invalidated at now(). No-op if
// the row is already invalidated (LEAST + COALESCE pattern preserves the
// earliest end time, which matches reality — once a cache is gone, it's gone).
func (chat *ConversationDao) SetCacheLifecycleInvalidated(ctx context.Context, cacheName string) error {
	if strings.TrimSpace(cacheName) == "" {
		return fmt.Errorf("cache_name is required")
	}

	query := `
		UPDATE llm_cache_lifecycle
		   SET invalidated_at = LEAST(COALESCE(invalidated_at, now()), now())
		 WHERE cache_name = $1
		   AND invalidated_at IS NULL
	`
	_, err := chat.dbManager.Db.ExecContext(ctx, query, cacheName)
	if err != nil {
		return fmt.Errorf("SetCacheLifecycleInvalidated: %w", err)
	}
	return nil
}

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
	if err := GetConversationDao().InsertCacheLifecycle(ctx, record); err != nil {
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
	if err := GetConversationDao().SetCacheLifecycleInvalidated(ctx, cacheName); err != nil {
		slog.Error("cache lifecycle invalidation update failed",
			"error", err,
			"cache_name", cacheName)
	}
}

// toUUIDOrNil converts a *string to sql.NullString suitable for a uuid column.
// Empty / nil → NULL. Trims whitespace.
func toUUIDOrNil(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

// toNullableText converts a *string to sql.NullString. Empty / nil → NULL.
func toNullableText(s *string) sql.NullString {
	if s == nil || strings.TrimSpace(*s) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// stringPtrIfNotEmpty is a helper for callers building CacheLifecycleRecord.
// Returns nil if the input is empty; otherwise a pointer to the string.
func stringPtrIfNotEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

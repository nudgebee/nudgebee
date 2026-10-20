package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	llmruntime "nudgebee/llm/llms/runtime"
)

// InsertCacheLifecycle records a new cache resource. Idempotent — duplicate
// inserts (same cache_name) are silently ignored to handle the rare race
// where two requests trigger createCache concurrently and the provider
// deduplicates by content hash.
func (chat *ConversationDao) InsertCacheLifecycle(ctx context.Context, record *llmruntime.CacheLifecycleRecord) error {
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

// setCacheLifecycleInvalidatedSQL marks a cache invalidated at now(), but only
// while it is still alive (invalidated_at IS NULL). The IS NULL guard makes the
// statement idempotent — a re-delete of an already-invalidated cache is a no-op,
// so the original (earlier) death time is preserved. Scoped to one cache_name
// (the provider resource id), never a whole slot.
const setCacheLifecycleInvalidatedSQL = `
		UPDATE llm_cache_lifecycle
		   SET invalidated_at = now()
		 WHERE cache_name = $1
		   AND invalidated_at IS NULL
	`

// SetCacheLifecycleInvalidated marks a cache as invalidated at now(). No-op if
// the row is already invalidated (LEAST + COALESCE pattern preserves the
// earliest end time, which matches reality — once a cache is gone, it's gone).
func (chat *ConversationDao) SetCacheLifecycleInvalidated(ctx context.Context, cacheName string) error {
	if strings.TrimSpace(cacheName) == "" {
		return fmt.Errorf("cache_name is required")
	}

	_, err := chat.dbManager.Db.ExecContext(ctx, setCacheLifecycleInvalidatedSQL, cacheName)
	if err != nil {
		return fmt.Errorf("SetCacheLifecycleInvalidated: %w", err)
	}
	return nil
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

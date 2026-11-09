-- Index the token usage rows whose conversation was deleted, so budget checks
-- find them without scanning (see V986: those rows are unlinked, not deleted,
-- and carry tenant_id). Only rows with no conversation and a stamped tenant
-- match, so the index stays small; calls that never had a conversation have
-- no tenant and are excluded.
--
-- A plain build scans llm_conversation_token_usage (~2 GB on dev) and blocks
-- inserts on it for the duration (a few seconds; V918/V983 built similar
-- indexes on this table in ~2 s). It is its own file so the build does not hold
-- V986's locks on llm_conversations. On managed environments pre-apply before
-- merge so this is a no-op:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_token_usage_orphaned_spend
--     ON llm_conversation_token_usage (created_at)
--     WHERE conversation_id IS NULL AND tenant_id IS NOT NULL;

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_orphaned_spend
    ON llm_conversation_token_usage (created_at)
    WHERE conversation_id IS NULL AND tenant_id IS NOT NULL;

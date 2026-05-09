-- V672: Cleanup deprecated token/model columns from llm_conversation_agent
-- These columns have been replaced by the llm_conversation_token_usage table (V588)
-- Data was backfilled in V589, and dual-write has been active since then.
-- All read queries (cost API, budget, usage metrics) already use llm_conversation_token_usage.

-- Step 1: Backfill missing entries that were never migrated to the new table.
-- This includes:
--   - ~252K rows from Oct 2024 – Sep 2025 (before llm_provider/llm_model columns were added)
--   - ~2.4K rows from Nov 2025 (gap between V589 backfill and dual-write deployment)
-- Removed the NULL filter on llm_provider/llm_model — COALESCE handles NULLs as 'unknown'.
-- cache_creation_tokens set to 0 — old schema had no equivalent column.
INSERT INTO llm_conversation_token_usage (
    conversation_id, message_id, agent_id, agent_name, account_id, user_id,
    llm_provider, llm_model,
    input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
    is_cache_hit, cache_hit_rate,
    retry_attempt, fallback_from_model, fallback_chain,
    latency_seconds, request_status, error_message,
    content_length, stop_reason,
    created_at, updated_at
)
SELECT
    a.conversation_id,
    a.message_id,
    a.id,
    a.agent_name,
    a.account_id,
    a.user_id,
    COALESCE(a.llm_provider, 'unknown'),
    COALESCE(a.llm_model, 'unknown'),
    COALESCE(a.input_tokens, 0),
    COALESCE(a.output_tokens, 0),
    COALESCE(a.cached_input_tokens, 0),
    0,  -- cache_creation_tokens: no equivalent in old schema, default to 0
    CASE WHEN COALESCE(a.cached_input_tokens, 0) > 0 THEN true ELSE false END,
    CASE
        WHEN COALESCE(a.input_tokens, 0) > 0
        THEN (COALESCE(a.cached_input_tokens, 0)::float8 / COALESCE(a.input_tokens, 0)::float8) * 100
        ELSE NULL
    END,
    0, NULL, NULL,
    NULL,
    CASE
        WHEN a.status = 'success' THEN 'success'
        WHEN a.status = 'fail' THEN 'failure'
        ELSE 'success'
    END,
    NULL, NULL, NULL,
    a.created_at, a.updated_at
FROM llm_conversation_agent a
WHERE (COALESCE(a.input_tokens, 0) > 0 OR COALESCE(a.output_tokens, 0) > 0)
  AND NOT EXISTS (
    SELECT 1 FROM llm_conversation_token_usage t WHERE t.agent_id = a.id
  );

-- Step 2: Verify backfill — fail the migration if any rows were missed
DO $$
DECLARE
    missing_count INT;
BEGIN
    SELECT COUNT(*) INTO missing_count
    FROM llm_conversation_agent a
    WHERE (COALESCE(a.input_tokens, 0) > 0 OR COALESCE(a.output_tokens, 0) > 0)
      AND NOT EXISTS (
        SELECT 1 FROM llm_conversation_token_usage t WHERE t.agent_id = a.id
      );

    IF missing_count > 0 THEN
        RAISE EXCEPTION 'Backfill verification failed: % agent rows still missing from llm_conversation_token_usage', missing_count;
    END IF;

    RAISE NOTICE 'Backfill verification passed: all agent token rows present in llm_conversation_token_usage';
END $$;

-- Step 3: Drop the index on cache token columns
DROP INDEX IF EXISTS idx_llm_conversation_agent_cache_tokens;

-- Step 4: Drop the deprecated columns
ALTER TABLE llm_conversation_agent
    DROP COLUMN IF EXISTS input_tokens,
    DROP COLUMN IF EXISTS output_tokens,
    DROP COLUMN IF EXISTS cached_input_tokens,
    DROP COLUMN IF EXISTS non_cached_input_tokens,
    DROP COLUMN IF EXISTS llm_model,
    DROP COLUMN IF EXISTS llm_provider;

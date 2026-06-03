-- Rollback Migration: V583 - Remove cache token columns from llm_conversation_agent table
-- This reverts the changes made in up.sql

-- Drop the index first
DROP INDEX IF EXISTS idx_llm_conversation_agent_cache_tokens;

-- Remove comments
COMMENT ON COLUMN llm_conversation_agent.input_tokens IS NULL;
COMMENT ON COLUMN llm_conversation_agent.output_tokens IS NULL;
COMMENT ON COLUMN llm_conversation_agent.cached_input_tokens IS NULL;
COMMENT ON COLUMN llm_conversation_agent.non_cached_input_tokens IS NULL;

-- Drop the new columns
ALTER TABLE llm_conversation_agent
DROP COLUMN IF EXISTS cached_input_tokens,
DROP COLUMN IF EXISTS non_cached_input_tokens;

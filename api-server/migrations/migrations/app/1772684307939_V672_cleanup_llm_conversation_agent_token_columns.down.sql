-- V672 Rollback: Re-add token/model columns to llm_conversation_agent
-- NOTE: Data in these columns will be lost after rollback. Only schema is restored.

ALTER TABLE llm_conversation_agent
    ADD COLUMN IF NOT EXISTS input_tokens integer NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens integer NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cached_input_tokens integer DEFAULT 0 NOT NULL,
    ADD COLUMN IF NOT EXISTS non_cached_input_tokens integer DEFAULT 0 NOT NULL,
    ADD COLUMN IF NOT EXISTS llm_model text,
    ADD COLUMN IF NOT EXISTS llm_provider text;

-- Re-create the index
CREATE INDEX IF NOT EXISTS idx_llm_conversation_agent_cache_tokens
ON llm_conversation_agent(cached_input_tokens, non_cached_input_tokens)
WHERE cached_input_tokens > 0;

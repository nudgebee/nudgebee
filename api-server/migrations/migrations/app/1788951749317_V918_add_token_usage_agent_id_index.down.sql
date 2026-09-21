-- Reverts V918. Also drops the index if it arrived via the out-of-band
-- CONCURRENTLY step.
DROP INDEX IF EXISTS idx_llm_conversation_token_usage_agent_id;

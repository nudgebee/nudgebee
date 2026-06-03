-- Add usage tracking columns to llm_conversation_memory.
-- use_count: incremented each time a memory is surfaced to an agent, enabling future pruning
--            of never-used or rarely-used memories.
-- last_used_at: timestamp of the most recent retrieval, used to detect stale memories.
ALTER TABLE llm_conversation_memory
    ADD COLUMN IF NOT EXISTS use_count   INT                      NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMP WITH TIME ZONE NULL;

CREATE INDEX IF NOT EXISTS idx_llm_conv_memory_last_used ON llm_conversation_memory(last_used_at);

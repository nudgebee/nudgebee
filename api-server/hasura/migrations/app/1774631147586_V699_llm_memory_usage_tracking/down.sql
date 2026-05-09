DROP INDEX IF EXISTS idx_llm_conv_memory_last_used;

ALTER TABLE llm_conversation_memory
    DROP COLUMN IF EXISTS use_count,
    DROP COLUMN IF EXISTS last_used_at;

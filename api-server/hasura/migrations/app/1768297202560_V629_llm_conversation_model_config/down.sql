-- Remove llm_provider and llm_model columns from llm_conversations table

DROP INDEX IF EXISTS idx_llm_conversations_model;

ALTER TABLE llm_conversations
DROP COLUMN IF EXISTS llm_provider,
DROP COLUMN IF EXISTS llm_model;

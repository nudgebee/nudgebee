-- Add llm_provider and llm_model columns to llm_conversations table
-- These columns store the user-selected model configuration for a conversation

ALTER TABLE llm_conversations
ADD COLUMN IF NOT EXISTS llm_provider VARCHAR(50),
ADD COLUMN IF NOT EXISTS llm_model VARCHAR(100);

-- Add index for querying conversations by model
CREATE INDEX IF NOT EXISTS idx_llm_conversations_model
ON llm_conversations(llm_provider, llm_model)
WHERE llm_provider IS NOT NULL;

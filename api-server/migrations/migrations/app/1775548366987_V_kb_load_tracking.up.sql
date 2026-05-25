-- Add document load tracking columns to llm_knowledgebases
ALTER TABLE llm_knowledgebases
ADD COLUMN IF NOT EXISTS document_count integer,
ADD COLUMN IF NOT EXISTS last_loaded_at timestamp;

COMMENT ON COLUMN llm_knowledgebases.document_count IS 'Number of documents from the last successful load';
COMMENT ON COLUMN llm_knowledgebases.last_loaded_at IS 'Timestamp of the last successful embedding generation';

-- Add load tracking metadata columns to rag_embedding_token_usage
ALTER TABLE rag_embedding_token_usage
ADD COLUMN IF NOT EXISTS knowledgebase_id text,
ADD COLUMN IF NOT EXISTS load_duration_seconds float8,
ADD COLUMN IF NOT EXISTS triggered_by text DEFAULT 'system',
ADD COLUMN IF NOT EXISTS trigger_type text DEFAULT 'system_sync',
ADD COLUMN IF NOT EXISTS module text,
ADD COLUMN IF NOT EXISTS expected_document_count int4;

COMMENT ON COLUMN rag_embedding_token_usage.knowledgebase_id IS 'FK to llm_knowledgebases.id (text, not enforced)';
COMMENT ON COLUMN rag_embedding_token_usage.load_duration_seconds IS 'Total wall-clock time for the embedding batch';
COMMENT ON COLUMN rag_embedding_token_usage.triggered_by IS 'User ID or system for automated loads';
COMMENT ON COLUMN rag_embedding_token_usage.trigger_type IS 'user_create, user_update, user_retrigger, or system_sync';
COMMENT ON COLUMN rag_embedding_token_usage.module IS 'Source module: knowledge_base, integration, etc.';
COMMENT ON COLUMN rag_embedding_token_usage.expected_document_count IS 'Number of documents submitted for embedding (before failures)';

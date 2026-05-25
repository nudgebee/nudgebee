ALTER TABLE llm_knowledgebases
DROP COLUMN IF EXISTS document_count,
DROP COLUMN IF EXISTS last_loaded_at;

ALTER TABLE rag_embedding_token_usage
DROP COLUMN IF EXISTS knowledgebase_id,
DROP COLUMN IF EXISTS load_duration_seconds,
DROP COLUMN IF EXISTS triggered_by,
DROP COLUMN IF EXISTS trigger_type,
DROP COLUMN IF EXISTS module,
DROP COLUMN IF EXISTS expected_document_count;

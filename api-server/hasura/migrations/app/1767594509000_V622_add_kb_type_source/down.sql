-- Remove kb_type, kb_source, and integration_id columns from llm_knowledgebases table
ALTER TABLE llm_knowledgebases DROP COLUMN IF EXISTS kb_type;
ALTER TABLE llm_knowledgebases DROP COLUMN IF EXISTS kb_source;
ALTER TABLE llm_knowledgebases DROP COLUMN IF EXISTS integration_id;

-- Drop indexes
DROP INDEX IF EXISTS idx_kb_type;
DROP INDEX IF EXISTS idx_kb_integration_id;
DROP INDEX IF EXISTS idx_kb_integration_unique;

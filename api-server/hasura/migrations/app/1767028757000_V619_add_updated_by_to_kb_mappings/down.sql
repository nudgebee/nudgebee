-- Remove updated_by column from llm_kb_agent_mappings
ALTER TABLE llm_kb_agent_mappings
DROP CONSTRAINT IF EXISTS kb_agent_mapping_updated_by_fkey;

ALTER TABLE llm_kb_agent_mappings
DROP COLUMN IF EXISTS updated_by;


-- Remove junction table and indexes
DROP INDEX IF EXISTS idx_kb_agent_mapping_account_id;
DROP INDEX IF EXISTS idx_kb_agent_mapping_agent_id;
DROP INDEX IF EXISTS idx_kb_agent_mapping_kb_id;

DROP TABLE IF EXISTS llm_kb_agent_mappings;

-- Rollback: Remove migrated data from KB system
-- This does NOT restore data to llm_rags (which was never deleted)

-- Revert agent_id rename from 'knowledge_base' back to 'docs'
UPDATE llm_rags
SET agent_id = 'docs',
    updated_at = NOW()
WHERE agent_id = 'knowledge_base';

-- Remove mappings for migrated RAGs
DELETE FROM llm_kb_agent_mappings
WHERE kb_id IN (
    SELECT id FROM llm_knowledgebases
    WHERE name LIKE 'migrated_%'
);

-- Remove migrated KBs
DELETE FROM llm_knowledgebases
WHERE name LIKE 'migrated_%';

-- Note: Original llm_rags data remains intact

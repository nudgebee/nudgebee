-- Migration: llm_rags → llm_knowledgebases + llm_kb_agent_mappings
-- This migration preserves RAG IDs to maintain ChromaDB collection compatibility
-- Collections named "agent_{agent_id}" will need to be renamed to "kb_{kb_id}"

-- Step 0: Rename legacy "docs" agent to "knowledge_base"
-- The docs agent was renamed to knowledge_base, update any existing records
UPDATE llm_rags
SET agent_id = 'knowledge_base',
    updated_at = NOW()
WHERE agent_id = 'docs';

-- Step 1: Migrate RAG data to llm_knowledgebases
-- Preserve original ID for ChromaDB collection mapping
INSERT INTO llm_knowledgebases (
    id,
    tenant_id,
    account_id,
    name,
    description,
    data,
    data_format,
    data_filename,
    data_size_bytes,
    status,
    created_by,
    updated_by,
    created_at,
    updated_at
)
SELECT DISTINCT ON (r.account_id, r.agent_id, COALESCE(r.data_filename, 'rag'))
    r.id,  -- Preserve original ID for RAG collection compatibility
    r.tenant_id,
    r.account_id,
    -- Generate unique name from agent_id and filename
    CONCAT('migrated_', r.agent_id, '_', COALESCE(r.data_filename, 'rag')),
    -- No description in llm_rags, set to NULL
    NULL,
    r.data,
    COALESCE(r.data_format, 'text'),
    COALESCE(r.data_filename, 'unknown'),
    LENGTH(r.data),  -- Calculate data size
    'active',  -- All migrated RAGs start as active
    r.created_by,
    r.updated_by,
    r.created_at,
    r.updated_at
FROM llm_rags r
WHERE NOT EXISTS (
    -- Avoid duplicates if migration runs multiple times
    -- Check both ID and unique constraint (account_id, name)
    SELECT 1 FROM llm_knowledgebases kb
    WHERE kb.id = r.id
       OR (kb.account_id = r.account_id
           AND kb.name = CONCAT('migrated_', r.agent_id, '_', COALESCE(r.data_filename, 'rag')))
)
ORDER BY r.account_id, r.agent_id, COALESCE(r.data_filename, 'rag'), r.created_at DESC;

-- Step 2: Create agent mappings in junction table
-- Map each migrated KB to its original agent
-- Only create mappings for KBs that were actually inserted in Step 1
INSERT INTO llm_kb_agent_mappings (
    kb_id,
    agent_id,
    account_id,
    created_by,
    updated_by,
    created_at,
    updated_at
)
SELECT
    r.id,  -- KB id (same as original rag id)
    r.agent_id,
    r.account_id,
    r.created_by,
    r.updated_by,
    r.created_at,
    r.updated_at
FROM llm_rags r
WHERE EXISTS (
    -- Only create mappings for KBs that exist (were created in Step 1)
    SELECT 1 FROM llm_knowledgebases kb WHERE kb.id = r.id
)
AND NOT EXISTS (
    -- Avoid duplicate mappings
    SELECT 1 FROM llm_kb_agent_mappings m
    WHERE m.kb_id = r.id AND m.agent_id = r.agent_id
);

-- Step 3: Verification comment
-- To verify migration success, run:
-- SELECT
--     (SELECT COUNT(*) FROM llm_rags) as rags_count,
--     (SELECT COUNT(*) FROM llm_knowledgebases WHERE name LIKE 'migrated_%') as migrated_kb_count,
--     (SELECT COUNT(*) FROM llm_kb_agent_mappings) as mapping_count;

-- Note: llm_rags table is NOT dropped to maintain backwards compatibility
-- RAG server collections named "agent_{agent_id}" should be manually migrated to "kb_{kb_id}"
-- or code should support dual lookup for backwards compatibility

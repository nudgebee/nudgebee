-- Add updated_by column to llm_kb_agent_mappings for audit trail
ALTER TABLE llm_kb_agent_mappings
ADD COLUMN IF NOT EXISTS updated_by UUID;

-- Add foreign key constraint to users table
ALTER TABLE llm_kb_agent_mappings
DROP CONSTRAINT IF EXISTS kb_agent_mapping_updated_by_fkey;

ALTER TABLE llm_kb_agent_mappings
ADD CONSTRAINT kb_agent_mapping_updated_by_fkey
FOREIGN KEY (updated_by)
REFERENCES public.users(id)
ON DELETE RESTRICT ON UPDATE RESTRICT;

-- Add comment explaining the column
COMMENT ON COLUMN llm_kb_agent_mappings.updated_by IS 'User who last modified the KB-agent mapping';

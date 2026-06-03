-- Add kb_type, kb_source, and integration_id columns to llm_knowledgebases table
ALTER TABLE llm_knowledgebases
ADD COLUMN kb_type VARCHAR(50) DEFAULT 'manual' NOT NULL,
ADD COLUMN kb_source VARCHAR(50),
ADD COLUMN integration_id UUID;

-- Add constraint to validate kb_type values
ALTER TABLE llm_knowledgebases
ADD CONSTRAINT kb_type_check CHECK (kb_type IN ('manual', 'integration'));

-- Add constraint to validate kb_source values (when not null)
ALTER TABLE llm_knowledgebases
ADD CONSTRAINT kb_source_check CHECK (kb_source IS NULL OR kb_source IN ('confluence', 'servicenow'));

-- Add foreign key to integrations table with CASCADE delete
ALTER TABLE llm_knowledgebases
ADD CONSTRAINT kb_integration_id_fkey FOREIGN KEY (integration_id)
    REFERENCES public.integrations(id) ON DELETE CASCADE ON UPDATE RESTRICT;

-- Add index for filtering by type
CREATE INDEX idx_kb_type ON llm_knowledgebases(kb_type);

-- Add index for integration lookups
CREATE INDEX idx_kb_integration_id ON llm_knowledgebases(integration_id);

-- Add unique constraint: one KB per integration per account
CREATE UNIQUE INDEX idx_kb_integration_unique ON llm_knowledgebases(account_id, integration_id) WHERE integration_id IS NOT NULL;

-- Add comments
COMMENT ON COLUMN llm_knowledgebases.kb_type IS 'KB type: manual (user-created) or integration (from external systems)';
COMMENT ON COLUMN llm_knowledgebases.kb_source IS 'Integration source: confluence, servicenow (null for manual KBs)';
COMMENT ON COLUMN llm_knowledgebases.integration_id IS 'Link to integrations table (null for manual KBs)';

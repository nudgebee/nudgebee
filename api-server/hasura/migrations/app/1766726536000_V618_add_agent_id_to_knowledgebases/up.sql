
-- Create junction table for many-to-many KB to Agent mapping
CREATE TABLE IF NOT EXISTS llm_kb_agent_mappings (
    kb_id UUID NOT NULL,
    agent_id VARCHAR(255) NOT NULL,
    account_id UUID NOT NULL,
    created_by UUID,
    created_at TIMESTAMP DEFAULT NOW() NOT NULL,
    updated_at TIMESTAMP DEFAULT NOW() NOT NULL,

    PRIMARY KEY (kb_id, agent_id),

    CONSTRAINT kb_agent_mapping_kb_fkey FOREIGN KEY (kb_id)
        REFERENCES llm_knowledgebases(id) ON DELETE CASCADE ON UPDATE RESTRICT,
    CONSTRAINT kb_agent_mapping_account_fkey FOREIGN KEY (account_id)
        REFERENCES public.cloud_accounts(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT kb_agent_mapping_created_by_fkey FOREIGN KEY (created_by)
        REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT
);

-- Create indexes for faster queries
CREATE INDEX IF NOT EXISTS idx_kb_agent_mapping_kb_id ON llm_kb_agent_mappings(kb_id);
CREATE INDEX IF NOT EXISTS idx_kb_agent_mapping_agent_id ON llm_kb_agent_mappings(agent_id);
CREATE INDEX IF NOT EXISTS idx_kb_agent_mapping_account_id ON llm_kb_agent_mappings(account_id);

-- Add comments
COMMENT ON TABLE llm_kb_agent_mappings IS 'Many-to-many mapping between knowledge bases and agents';
COMMENT ON COLUMN llm_kb_agent_mappings.kb_id IS 'Knowledge base ID';
COMMENT ON COLUMN llm_kb_agent_mappings.agent_id IS 'Agent name/ID';
COMMENT ON COLUMN llm_kb_agent_mappings.account_id IS 'Account ID for access control';


CREATE TABLE IF NOT EXISTS llm_knowledgebases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
    tenant_id UUID NOT NULL,
    account_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    data TEXT NOT NULL,
    data_format VARCHAR(50) NOT NULL,
    data_filename VARCHAR(255) NOT NULL,
    data_size_bytes BIGINT,
    status VARCHAR(50) DEFAULT 'active' NOT NULL,
    created_by UUID,
    updated_by UUID,
    created_at TIMESTAMP DEFAULT NOW() NOT NULL,
    updated_at TIMESTAMP DEFAULT NOW() NOT NULL,

    CONSTRAINT kb_tenant_id_fkey FOREIGN KEY (tenant_id)
        REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT kb_account_id_fkey FOREIGN KEY (account_id)
        REFERENCES public.cloud_accounts(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT kb_created_by_fkey FOREIGN KEY (created_by)
        REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT kb_updated_by_fkey FOREIGN KEY (updated_by)
        REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT kb_name_account_unique UNIQUE (account_id, name),
    CONSTRAINT kb_status_check CHECK (status IN ('active', 'archived', 'processing', 'error'))
);

CREATE INDEX idx_kb_account_id ON llm_knowledgebases(account_id);
CREATE INDEX idx_kb_tenant_id ON llm_knowledgebases(tenant_id);
CREATE INDEX idx_kb_status ON llm_knowledgebases(status);

COMMENT ON TABLE llm_knowledgebases IS 'Account-scoped knowledge bases for semantic search and RAG';
COMMENT ON COLUMN llm_knowledgebases.data IS 'Full file content stored as TEXT';
COMMENT ON COLUMN llm_knowledgebases.data_format IS 'File format: json, xml, csv, text, or pdf';
COMMENT ON COLUMN llm_knowledgebases.status IS 'Processing status: active (ready), processing (creating embeddings), error (failed), or archived (inactive)';

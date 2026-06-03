
CREATE TABLE IF NOT EXISTS llm_global_contexts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
    tenant_id UUID NOT NULL,
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

    CONSTRAINT gc_tenant_id_fkey FOREIGN KEY (tenant_id)
        REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT gc_created_by_fkey FOREIGN KEY (created_by)
        REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT gc_updated_by_fkey FOREIGN KEY (updated_by)
        REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT gc_name_tenant_unique UNIQUE (tenant_id, name),
    CONSTRAINT gc_status_check CHECK (status IN ('active', 'archived', 'processing', 'error'))
);

CREATE INDEX idx_gc_tenant_id ON llm_global_contexts(tenant_id);
CREATE INDEX idx_gc_status ON llm_global_contexts(status);

COMMENT ON TABLE llm_global_contexts IS 'Tenant-scoped global context files for whole-file retrieval (no semantic search)';
COMMENT ON COLUMN llm_global_contexts.data IS 'Full file content stored as TEXT (not chunked or embedded)';
COMMENT ON COLUMN llm_global_contexts.data_format IS 'File format: json, xml, csv, or text';
COMMENT ON COLUMN llm_global_contexts.status IS 'Status: active, processing, error, or archived';

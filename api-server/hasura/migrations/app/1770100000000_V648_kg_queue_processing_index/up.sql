-- Index for efficient tenant last_sync_time lookups used by KG queue deduplication
CREATE INDEX IF NOT EXISTS idx_kg_filters_tenant_last_sync
ON knowledge_graph_tenant_filters(tenant_id, last_sync_time)
WHERE enabled = true;

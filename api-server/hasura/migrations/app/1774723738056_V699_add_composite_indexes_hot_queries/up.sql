-- Composite indexes for hot query paths (see #26794)
-- IMPORTANT: Do not use CREATE INDEX CONCURRENTLY — Hasura runs migrations
-- inside a transaction block which does not support it.

-- events: triage classification lookups by fingerprint + account + status
-- Used by: triage/classification.go, triage/bulk_operations.go
CREATE INDEX IF NOT EXISTS idx_events_fingerprint_account_status
    ON events (fingerprint, cloud_account_id, nb_status);

-- integrations: duplicate-check and type lookups by type + tenant + source
-- Used by: integrations/core/integration_config.go
CREATE INDEX IF NOT EXISTS idx_integrations_type_tenant_source
    ON integrations (type, tenant_id, source)
    WHERE status != 'disabled';

-- knowledge_graph_node: stale node cleanup by tenant + sync version + level
-- Used by: knowledge_graph/core/service.go (markStaleNodesInactive)
-- Column order: equality columns (tenant_id, level) before range (last_sync_version).
-- is_active omitted from B-tree since the partial WHERE clause already guarantees it.
CREATE INDEX IF NOT EXISTS idx_kg_node_tenant_sync_level_active
    ON knowledge_graph_node (tenant_id, level, last_sync_version)
    WHERE is_active = true;

-- Revert V825: restore the tenant-less name btrees (V705 shape) and drop the tenant-leading indexes.
-- Plain DDL (no CONCURRENTLY) per the repo migration rule; pre-apply the CONCURRENTLY form on live
-- envs before merge if you need a non-blocking revert.

CREATE INDEX IF NOT EXISTS idx_kg_node_qa_name
    ON knowledge_graph_node ((query_attributes->>'name'))
    WHERE is_active = true;

CREATE INDEX IF NOT EXISTS idx_kg_node_qa_name_ns
    ON knowledge_graph_node ((query_attributes->>'name'), (query_attributes->>'namespace'))
    WHERE is_active = true;

DROP INDEX IF EXISTS idx_kg_node_tenant_qa_name_ns;
DROP INDEX IF EXISTS idx_kg_node_tenant_qa_name_trgm;

-- pg_trgm / btree_gin are left installed (harmless; may be used by other objects).

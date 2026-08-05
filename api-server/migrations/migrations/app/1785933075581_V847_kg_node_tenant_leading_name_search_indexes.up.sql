-- Knowledge Graph node-search indexes: make name search tenant-scoped and fuzzy-capable.
--
-- kg_search_nodes / kg_list_nodes (the LLM discovery tool) is ALWAYS single-tenant, but the
-- existing name indexes (V705: idx_kg_node_qa_name, idx_kg_node_qa_name_ns) are NOT
-- tenant-leading, so the planner applies tenant_id as a post-scan filter, and ILIKE (the
-- agent's fuzzy pattern) can't use a plain btree at all -> a near-full scan of the tenant's
-- active nodes. Measured DB-side on the dev DB (PG17): the fuzzy path grows linearly with
-- tenant size (~9ms @5K -> ~35ms @25K -> ~90ms @100K) and SearchNodes runs it twice
-- (COUNT + SELECT). Two tenant-leading indexes fix both dimensions:
--   * exact/prefix: a (tenant_id, name, namespace) btree that supersedes the two tenant-less
--     name btrees below (every kg_search_nodes query carries tenant_id).
--   * fuzzy ILIKE: a trigram GIN keyed (tenant_id, name) so '%x%' / 'x%' is a tenant-scoped
--     index probe -> ~9ms @100K (~10x, and the gap widens with scale).
--
-- CONCURRENTLY is NOT allowed in migration files here (golang-migrate wraps each migration in a
-- transaction). PRE-APPLY the CONCURRENTLY form manually on each live env (main/test/prod)
-- BEFORE merge — see the PR body — so IF NOT EXISTS makes these statements a no-op there. On
-- fresh/small DBs the plain statements below build quickly.

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gin;

-- Tenant-leading exact + name/namespace lookups (supersedes idx_kg_node_qa_name and idx_kg_node_qa_name_ns).
CREATE INDEX IF NOT EXISTS idx_kg_node_tenant_qa_name_ns
    ON knowledge_graph_node (tenant_id, (query_attributes->>'name'), (query_attributes->>'namespace'))
    WHERE is_active = true;

-- Tenant-leading trigram GIN for ILIKE name search (btree_gin supplies the tenant_id opclass).
CREATE INDEX IF NOT EXISTS idx_kg_node_tenant_qa_name_trgm
    ON knowledge_graph_node USING gin (tenant_id, (query_attributes->>'name') gin_trgm_ops)
    WHERE is_active = true;

-- Drop the now-redundant tenant-less name btrees (fully covered by idx_kg_node_tenant_qa_name_ns).
DROP INDEX IF EXISTS idx_kg_node_qa_name;
DROP INDEX IF EXISTS idx_kg_node_qa_name_ns;

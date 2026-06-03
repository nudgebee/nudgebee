-- Seed the USES_SERVICE_ACCOUNT relationship type into knowledge_graph_relationship_types.
--
-- The Go enum core.RelationshipUsesServiceAccount = "USES_SERVICE_ACCOUNT"
-- was introduced by PR #31034 (K8sServiceAccount + IRSA chain) but the
-- corresponding FK-target row was never added to this table. As soon as
-- the build started emitting Workload → USES_SERVICE_ACCOUNT → K8sServiceAccount
-- edges, EVERY edge-save batch began rolling back with:
--
--   pq: insert or update on table "knowledge_graph_edge"
--   violates foreign key constraint "knowledge_graph_edge_relationship_type_fkey" (23503)
--
-- Because SaveEdges does a single transactional batch insert (~2000 edges per
-- build), the FK violation on this one row tombstoned the entire edge save —
-- producing tenants with 0 active edges across the board. See .vscode/kg.logs
-- 2026-05-26T19:32:32 for the production stack trace.
INSERT INTO knowledge_graph_relationship_types (name, value)
VALUES
    ('USES_SERVICE_ACCOUNT', 'USES_SERVICE_ACCOUNT')
ON CONFLICT (name) DO NOTHING;

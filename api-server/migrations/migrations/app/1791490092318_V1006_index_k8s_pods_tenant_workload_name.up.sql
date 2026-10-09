-- podOwnerLookupSQL (event/service.go) resolves an alert's subject to its
-- owning workload. The subject may be a pod name, a ReplicaSet name or the
-- workload's own name, so the query matches k8s_pods on
--   name = $3 OR workload_name = $3 OR name LIKE '<subject>-%'
-- idx_k8s_pods_tenant_name (V1005) covers the two name arms. Without an index
-- for the workload_name arm the planner cannot combine them and scans the whole
-- table again. Part of #38283.
--
-- Pre-build on every live database BEFORE this migration reaches it, then check
-- that the index is valid and has this definition (a failed concurrent build
-- leaves an INVALID index, and IF NOT EXISTS below would silently keep it):
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_k8s_pods_tenant_workload_name
--     ON public.k8s_pods (tenant_id, workload_name);
--   SELECT indisvalid, pg_get_indexdef(indexrelid) FROM pg_index
--    WHERE indexrelid = 'idx_k8s_pods_tenant_workload_name'::regclass;
-- Where it was pre-built this only takes a brief lock on k8s_pods. Elsewhere it
-- is a plain build that blocks writes to k8s_pods for one pass over the table
-- (a few seconds at 630k rows). If the lock cannot be taken within 5 seconds
-- the migration fails without changing anything; run the upgrade again.

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_k8s_pods_tenant_workload_name
    ON public.k8s_pods (tenant_id, workload_name);

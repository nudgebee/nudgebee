-- The workload side of podOwnerLookupSQL (event/service.go). It joins the
-- matched pods to k8s_workloads on tenant and namespace, by exact name and kind
-- or, for a ReplicaSet subject, to the Deployments of that namespace. The only
-- index that holds namespace, name and kind leads with cloud_account_id, which
-- this tenant-wide lookup does not have, so each probe read the full index or
-- the full table. With this index the join can read index entries only
-- (measured on a copy of real rows, together with V1005 and V1006: 18k buffers
-- per call down to about 200 on a freshly vacuumed table, 4.7k before the
-- vacuum). Part of #38283.
--
-- Pre-build on every live database BEFORE this migration reaches it, then check
-- that the index is valid and has this definition (a failed concurrent build
-- leaves an INVALID index, and IF NOT EXISTS below would silently keep it):
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_k8s_workloads_tenant_namespace_kind_name
--     ON public.k8s_workloads (tenant_id, namespace, kind, name);
--   SELECT indisvalid, pg_get_indexdef(indexrelid) FROM pg_index
--    WHERE indexrelid = 'idx_k8s_workloads_tenant_namespace_kind_name'::regclass;
-- Where it was pre-built this only takes a brief lock on k8s_workloads. Elsewhere it
-- is a plain build that blocks writes to k8s_workloads for one pass over the table
-- (about a second at 140k rows). If the lock cannot be taken within 5 seconds
-- the migration fails without changing anything; run the upgrade again.

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_k8s_workloads_tenant_namespace_kind_name
    ON public.k8s_workloads (tenant_id, namespace, kind, name);

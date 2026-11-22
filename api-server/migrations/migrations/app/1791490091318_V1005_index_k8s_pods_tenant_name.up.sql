-- Several alert paths look a pod up by name to find the workload that owns it:
-- webhook subject resolution (integrations/core/subject_resolution.go,
-- integrations/core/node_subject.go, integrations/datadog_webhook.go), the event
-- owner lookup (podOwnerLookupSQL in event/service.go) and the k8s collector's
-- event handler. No index on k8s_pods holds the pod name. These lookups also
-- include pods that have stopped, because an alert often arrives after its pod
-- is gone, so the partial "active" indexes cannot serve them. Each call read
-- the whole table (measured: 128k buffers on a 630k-row table for one row).
--
-- text_pattern_ops because podOwnerLookupSQL also finds a ReplicaSet's pods by
-- prefix (name LIKE '<replicaset>-%'), which a default btree cannot serve under
-- a non-C collation. Equality lookups use this operator class too. Keyed on
-- tenant_id because podOwnerLookupSQL searches the tenant, not one account; the
-- other callers pass tenant_id as well. Part of #38283.
--
-- Pre-build on every live database BEFORE this migration reaches it, then check
-- that the index is valid and has this definition (a failed concurrent build
-- leaves an INVALID index, and IF NOT EXISTS below would silently keep it):
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_k8s_pods_tenant_name
--     ON public.k8s_pods (tenant_id, name text_pattern_ops);
--   SELECT indisvalid, pg_get_indexdef(indexrelid) FROM pg_index
--    WHERE indexrelid = 'idx_k8s_pods_tenant_name'::regclass;
-- Where it was pre-built this only takes a brief lock on k8s_pods. Elsewhere it
-- is a plain build that blocks writes to k8s_pods for one pass over the table
-- (a few seconds at 630k rows). If the lock cannot be taken within 5 seconds
-- the migration fails without changing anything; run the upgrade again.

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_k8s_pods_tenant_name
    ON public.k8s_pods (tenant_id, name text_pattern_ops);

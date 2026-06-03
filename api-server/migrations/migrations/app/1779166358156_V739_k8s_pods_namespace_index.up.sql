-- Speed up security recommendation queries filtered by namespace.
-- Without this index the planner scans all pods for the account and filters
-- namespace as a post-scan predicate. With the index it seeks directly to
-- (cloud_account_id, tenant_id, namespace) and reads only the matching rows
-- before expanding the containers JSONB array.
CREATE INDEX IF NOT EXISTS idx_k8s_pods_active_account_namespace
ON k8s_pods (cloud_account_id, tenant_id, namespace)
WHERE is_active IS NOT FALSE;

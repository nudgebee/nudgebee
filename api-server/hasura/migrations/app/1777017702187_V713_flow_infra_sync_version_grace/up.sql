-- Grace bump for flow-sourced infra-authoritative rows.
--
-- Background: before this change, knowledge_graph_node rows with
-- (properties->>'source') in ('ebpf', 'datadog-apm', 'traces') were permanently
-- excluded from the sync-version sweep in Service.markInactiveNodes, regardless
-- of node_type. The Go code is now extended to admit rows whose node_type is
-- an "infra-authoritative" K8s-shaped type (CronJob, Job, Pod, K8sService,
-- Ingress, Namespace, Node, Workload) into that sweep.
--
-- Legacy rows of those types were stamped with old last_sync_version values.
-- Without this migration, the very first post-deploy sync would see them as
-- stale and mass-tombstone them. This UPDATE bumps each affected row's
-- last_sync_version to (its tenant's default filter.last_sync_version + 1) so
-- the first real sync treats them as up-to-date. They'll age out naturally
-- over subsequent syncs if the flow source stops observing them.
--
-- Apply this migration BEFORE deploying the services-server binary that
-- contains the extended predicate, or in the same rollout — the ordering
-- matters, because the new binary's first sync depends on these bumped
-- versions to avoid the mass-tombstone.
--
-- Safe to re-run: idempotent for any tenant whose default filter version
-- hasn't advanced past the bumped value.

UPDATE knowledge_graph_node n
SET last_sync_version = f.last_sync_version + 1
FROM (
    SELECT DISTINCT ON (tenant_id) tenant_id, last_sync_version
    FROM knowledge_graph_tenant_filters
    WHERE is_default = true
      AND enabled = true
    ORDER BY tenant_id, created_at ASC
) f
WHERE n.tenant_id = f.tenant_id
  AND n.is_active = true
  AND n.level = 'Tenant'
  AND n.node_type IN (
      'CronJob', 'Job', 'Pod', 'K8sService',
      'Ingress', 'Namespace', 'Node', 'Workload'
  )
  AND (n.properties->>'source') IN ('ebpf', 'datadog-apm', 'traces')
  AND n.last_sync_version IS DISTINCT FROM (f.last_sync_version + 1);

-- One-time cleanup of ExternalService nodes wrongly minted by the traces flow
-- source's pod-name matching bug (nudgebee-enterprise#34874), fixed in the
-- same PR as this migration. Trace spans report the raw pod hostname (e.g.
-- llm-server-57744b6758-z5422) rather than the owning workload name, so the
-- exact-match lookup against existing nodes always failed and a fresh
-- ExternalService node was created per pod replica/restart, going forward
-- indefinitely (flow-source nodes are not swept by the periodic
-- InfraAuthoritativeNodeTypes staleness pass, so these do not self-heal).
--
-- Discriminator: an ExternalService node is a pod-name duplicate iff (a) its
-- name matches the same pod-name shape the application code detects
-- (isPodName in flow_sources/ebpf_flow_source.go: `{name}-{8-10 char hash}-
-- {5 char id}` for Deployments, or `{name}-{5 char hash}-{5 char id}` for
-- Jobs/CronJobs), and (b) a real node already exists in the same tenant +
-- cloud account whose name equals the pod name with that suffix stripped
-- (the owning Workload/K8sService/Service — i.e. core.ExtractPodOwner's
-- result). Condition (b) is deliberately required: it is what distinguishes
-- a genuine duplicate from a coincidental external hostname that happens to
-- match the same shape (e.g. some AWS resource identifiers) with no
-- corresponding internal node — those are left alone, matching the
-- conservative discriminator style used in V802.
--
-- Soft-deactivate (is_active = false) rather than DELETE, matching the
-- existing tombstone convention (markInactiveNodes / MarkStaleEdgesInactive /
-- V802) so the rows remain available for audit/recovery. Idempotent:
-- re-running finds nothing left to update once applied.
WITH pod_named AS (
    SELECT
        n.id,
        n.tenant_id,
        n.cloud_account_id,
        n.properties ->> 'name' AS name,
        CASE
            WHEN (n.properties ->> 'name') ~ '-[a-z0-9]{8,10}-[a-z0-9]{5}$'
                THEN regexp_replace(n.properties ->> 'name', '-[a-z0-9]{8,10}-[a-z0-9]{5}$', '')
            WHEN (n.properties ->> 'name') ~ '-[a-z0-9]{5}-[a-z0-9]{5}$'
                THEN regexp_replace(n.properties ->> 'name', '-[a-z0-9]{5}-[a-z0-9]{5}$', '')
        END AS owner_name
    FROM knowledge_graph_node n
    WHERE n.node_type = 'ExternalService'
      AND n.source = 'traces'
      AND n.is_active = true
      AND (
          (n.properties ->> 'name') ~ '-[a-z0-9]{8,10}-[a-z0-9]{5}$'
          OR (n.properties ->> 'name') ~ '-[a-z0-9]{5}-[a-z0-9]{5}$'
      )
),
duplicates AS (
    SELECT pn.id
    FROM pod_named pn
    WHERE EXISTS (
        SELECT 1
        FROM knowledge_graph_node owner
        WHERE owner.tenant_id = pn.tenant_id
          AND owner.cloud_account_id = pn.cloud_account_id
          AND owner.node_type IN ('Workload', 'K8sService', 'Service')
          AND owner.is_active = true
          AND owner.properties ->> 'name' = pn.owner_name
    )
)
UPDATE knowledge_graph_node
SET is_active = false
WHERE id IN (SELECT id FROM duplicates)
  AND is_active = true;

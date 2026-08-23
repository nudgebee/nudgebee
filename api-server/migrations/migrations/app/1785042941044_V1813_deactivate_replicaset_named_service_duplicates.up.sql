-- One-time cleanup of Service nodes wrongly minted by the traces flow
-- source's application-matching bug (nudgebee-enterprise#34874, second gap
-- fixed in the same PR as this migration). OTel auto-instrumentation
-- sometimes reports service.name as the k8s ReplicaSet's own name (e.g.
-- cloud-collector-server-5888444974) rather than a stable service identity,
-- when OTEL_SERVICE_NAME isn't configured on the workload. Both
-- matchServiceApplicationToNode (exact-match lookup) and
-- createNodeForServiceApplication (node creation) used this raw name
-- unnormalized, so every ReplicaSet recreation (new rollout) minted a fresh
-- duplicate Service node instead of resolving to the real workload.
--
-- Discriminator: a Service node is a ReplicaSet-name duplicate iff (a) its
-- name ends in a single trailing 8-10 char lowercase-alphanumeric segment
-- (the shape isReplicaSetHashName in flow_sources/traces_flow_source.go
-- detects — a k8s ReplicaSet hash), and (b) a real node already exists in
-- the same tenant + cloud account whose name equals the name with that
-- segment stripped. Condition (b) is required for the same reason as V812:
-- a single trailing segment has less structure to confirm it's
-- machine-generated than the two-segment pod pattern, so some ordinary
-- hyphenated names coincidentally collide with the shape (e.g.
-- "fraud-detection" -> "detection" looks like a hash) — those are left
-- alone when no matching owner exists, rather than guessed at.
--
-- Soft-deactivate (is_active = false) rather than DELETE, matching the
-- existing tombstone convention (markInactiveNodes / MarkStaleEdgesInactive
-- / V802 / V812) so the rows remain available for audit/recovery.
-- Idempotent: re-running finds nothing left to update once applied.
WITH replicaset_named AS (
    SELECT
        n.id,
        n.tenant_id,
        n.cloud_account_id,
        n.properties ->> 'name' AS name,
        regexp_replace(n.properties ->> 'name', '-[a-z0-9]{8,10}$', '') AS owner_name
    FROM knowledge_graph_node n
    WHERE n.node_type = 'Service'
      AND n.source = 'traces'
      AND n.is_active = true
      AND (n.properties ->> 'name') ~ '-[a-z0-9]{8,10}$'
),
duplicates AS (
    SELECT rn.id
    FROM replicaset_named rn
    WHERE EXISTS (
        SELECT 1
        FROM knowledge_graph_node owner
        WHERE owner.tenant_id = rn.tenant_id
          AND owner.cloud_account_id = rn.cloud_account_id
          AND owner.node_type IN ('Workload', 'K8sService', 'Service')
          AND owner.is_active = true
          AND owner.id <> rn.id
          AND owner.properties ->> 'name' = rn.owner_name
    )
)
UPDATE knowledge_graph_node
SET is_active = false
WHERE id IN (SELECT id FROM duplicates)
  AND is_active = true;

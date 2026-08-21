-- Deactivate pre-existing false cross-namespace CALLS edges minted by the
-- eBPF flow source's name-only match fallback (nudgebee-enterprise#34639),
-- fixed in the same PR as this migration. The matching bug is fixed in code
-- going forward; this is a one-time cleanup of already-persisted bad rows,
-- which would otherwise sit active for up to KGEdgeStaleAfterDays (7 days)
-- before the normal staleness sweep clears them.
--
-- Discriminator (validated against live data while diagnosing #34639): a
-- CALLS edge is false iff the namespace actually observed for an endpoint
-- (properties.source_namespace / dest_namespace, set from the raw eBPF
-- observation) disagrees with the namespace of the node that endpoint
-- resolved to. Endpoints are matched by name rather than by edge column
-- position, since upstream-derived edges' properties.source_service/
-- dest_service do not always align with source_node_id/destination_node_id
-- in the same order. Deliberately conservative: an edge whose observed name
-- doesn't cleanly string-match either resolved node's name is left alone
-- (ambiguous, not flagged) rather than guessed at. Likewise, a resolved node
-- whose own namespace property is NULL (e.g. HelmChart — cluster-scoped, not
-- namespace-scoped) is not compared at all: IS DISTINCT FROM would otherwise
-- treat "no namespace on this node type" as a mismatch against any non-null
-- observed namespace, which is a property-shape gap, not a resolution bug.
--
-- Soft-deactivate (is_active = false) rather than DELETE, matching the
-- existing tombstone convention (markInactiveNodes / MarkStaleEdgesInactive)
-- so the rows remain available for audit/recovery. Idempotent: re-running
-- finds nothing left to update once applied.
WITH candidates AS (
    SELECT
        e.id,
        e.properties ->> 'source_service' AS obs_source_name,
        e.properties ->> 'source_namespace' AS obs_source_ns,
        e.properties ->> 'dest_service' AS obs_dest_name,
        e.properties ->> 'dest_namespace' AS obs_dest_ns,
        src.properties ->> 'name' AS src_node_name,
        src.properties ->> 'namespace' AS src_node_ns,
        dst.properties ->> 'name' AS dst_node_name,
        dst.properties ->> 'namespace' AS dst_node_ns
    FROM knowledge_graph_edge e
    JOIN knowledge_graph_node src ON src.id = e.source_node_id
    JOIN knowledge_graph_node dst ON dst.id = e.destination_node_id
    WHERE e.relationship_type = 'CALLS'
      AND e.source = 'ebpf'
      AND e.is_active = true
),
mismatched AS (
    SELECT id
    FROM candidates
    WHERE
        (obs_source_ns IS NOT NULL AND (
            (obs_source_name = src_node_name AND src_node_ns IS NOT NULL AND obs_source_ns IS DISTINCT FROM src_node_ns)
            OR (obs_source_name = dst_node_name AND dst_node_ns IS NOT NULL AND obs_source_ns IS DISTINCT FROM dst_node_ns)
        ))
        OR
        (obs_dest_ns IS NOT NULL AND (
            (obs_dest_name = src_node_name AND src_node_ns IS NOT NULL AND obs_dest_ns IS DISTINCT FROM src_node_ns)
            OR (obs_dest_name = dst_node_name AND dst_node_ns IS NOT NULL AND obs_dest_ns IS DISTINCT FROM dst_node_ns)
        ))
)
UPDATE knowledge_graph_edge
SET is_active = false
WHERE id IN (SELECT id FROM mismatched)
  AND is_active = true;

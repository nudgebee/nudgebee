-- Retire the eBPF-observed Service nodes that short-lived operator objects left
-- behind (#41003).
--
-- The eBPF agent labels every observed application with a `kind`. Kinds it does
-- not recognise used to fall through to NodeTypeService, and because each
-- instance of an ephemeral object carries a fresh random pod name, every one of
-- them minted its own node: GitHub ARC's EphemeralRunner (one per CI job) and
-- cert-manager's Challenge (one per certificate renewal). Nothing retired them.
-- NodeTypeService is not in core.InfraAuthoritativeNodeTypes, so markInactiveNodes
-- skips it; the time-based sweeps cover only ExternalService and the VM source;
-- and nb.CleanupData deletes only rows that are already inactive. On the dev
-- cluster that reached 10,063 finished CI runners against 87 real services.
--
-- The code fix (ebpfKindMap is now an allowlist — flow_sources/ebpf_flow_source.go)
-- stops new ones being created. This retires the ones already stored.
--
-- After that fix, kind='service' is the only kind that legitimately produces a
-- Service node from this source: every other recognised kind now maps to
-- Workload, Job, CronJob, Database, ExternalService, Node or CRD, and every
-- unrecognised kind is refused. So "node_type Service with a kind that is not
-- service" is exactly the set this defect produced, and the predicate cannot
-- drift away from the Go map.
--
-- Tombstone only; the rows are not deleted here. nb.CleanupData reclaims an
-- inactive node once no active edge points at it and it is older than
-- nb_retention_days_kg_inactive_nodes. updated_at is deliberately NOT bumped so
-- that job keeps using true last-seen time as its retention anchor, exactly as
-- MarkStaleEdgesInactive does — bumping it would restart the retention clock on
-- rows that have been dead for months.
--
-- Lock window: two single-table UPDATEs inside one transaction (Atlas runs each
-- migration file in a transaction, and this repo cannot batch or COMMIT per
-- chunk). Row-level write locks only, on rows a concurrent Knowledge Graph build
-- does not touch. Measured scope on the largest known install (dev): 10,104
-- node rows and 281 edge rows. The edge count is small only because the
-- existing behavioural-edge sweep had already retired 32,809 of them by age;
-- the 281 remaining belong to the recently-observed operator CRDs, whose nodes
-- the next build re-creates correctly typed. Expected duration well under a
-- second; worst case is bounded by the number of nodes the defect created.

-- Edges first, so no window exists in which an active edge points at a
-- tombstoned node (traversal treats those endpoints as live).
UPDATE knowledge_graph_edge e
SET is_active = false
WHERE e.is_active = true
  AND EXISTS (
    SELECT 1 FROM knowledge_graph_node n
    WHERE n.id IN (e.source_node_id, e.destination_node_id)
      AND n.is_active = true
      AND n.source = 'ebpf'
      AND n.node_type = 'Service'
      AND lower(n.properties->>'kind') IS DISTINCT FROM 'service'
  );

UPDATE knowledge_graph_node
SET is_active = false
WHERE is_active = true
  AND source = 'ebpf'
  AND node_type = 'Service'
  AND lower(properties->>'kind') IS DISTINCT FROM 'service';

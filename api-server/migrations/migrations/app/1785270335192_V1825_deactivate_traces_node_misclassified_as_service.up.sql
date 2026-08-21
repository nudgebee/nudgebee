-- Deactivate pre-existing phantom Service nodes created by the traces flow
-- source for K8s worker nodes observed via host-network/kubelet-level
-- traffic (e.g. a Prometheus node-metrics scrape) — nudgebee-enterprise#34880.
--
-- traces/service_map.go hardcoded ServiceApplicationId.Kind = "Service" for
-- every workload built from a span, regardless of the raw workload_namespace
-- value. The trace pipeline uses the literal namespace "node" as a sentinel
-- meaning the observed workload is actually the K8s Node itself (Node
-- objects are cluster-scoped and have no real namespace) — a convention
-- ebpf_flow_source.go's inferNodeType already honors via its "node" kind, but
-- traces never surfaced it. Fixed in the same PR as this migration
-- (applicationKindFor in service_map.go + the missing "Node" entry in
-- traces_flow_source.go's inferNodeType kindMap).
--
-- Discriminator (validated against live data while diagnosing this): every
-- one of the 100 affected rows found on dev has a properties.name matching
-- a GKE node hostname (gke-<cluster>-<pool>-<hash>-<suffix>) — deliberately
-- not hardcoded into the WHERE clause below, since the real invariant is the
-- namespace="node" sentinel itself, not the GKE-specific hostname shape (a
-- non-GKE K8s node would carry the same sentinel with a different hostname).
--
-- Soft-deactivate (is_active = false) rather than DELETE, matching the
-- existing tombstone convention (markInactiveNodes) and the precedent set by
-- the sibling V815 migration for this same issue. Idempotent: re-running
-- finds nothing left to update once applied.
UPDATE knowledge_graph_node
SET is_active = false
WHERE node_type = 'Service'
  AND source = 'traces'
  AND is_active = true
  AND query_attributes ->> 'namespace' = 'node';

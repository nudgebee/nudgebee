-- Deactivate pre-existing phantom MessageQueue/Database/Cache nodes minted by
-- traces' detectApplicationType span-name fallback (nudgebee-enterprise#34880),
-- fixed in the same PR as this migration. The classifier is fixed going
-- forward (it no longer trusts a CLIENT/PRODUCER span's name, since that name
-- describes the destination/operation being invoked, not the calling
-- service's own identity); this is a one-time cleanup of already-persisted
-- bad rows.
--
-- Discriminator (validated against live data while diagnosing #34880): every
-- OTHER classification path in detectApplicationType (messaging.system,
-- db-name patterns, service-name patterns) requires the service's own name to
-- contain the matched technology keyword — only the now-fixed span-name
-- fallback could produce a node whose properties.types keyword(s) are absent
-- from its own properties.name. That mismatch is therefore a reliable
-- fingerprint of this specific bug, not a guess: e.g. cloud-collector-server/
-- services-server/llm-server (Nudgebee's own k8s Deployments, confirmed via
-- their k8s-sourced Workload nodes) misclassified as rabbitmq because an
-- outbound span happened to be named "rabbitmq.publish"; likewise
-- elastic-operator (the ECK operator, which manages Elasticsearch but isn't
-- one) misclassified as a Database via an "elasticsearch"-named span.
--
-- Residual risk (accepted): a genuinely-classified node whose evidence came
-- from a non-outbound span (still valid post-fix) but whose own name happens
-- not to contain the matched keyword would also match this discriminator,
-- since historical rows don't retain the span's direction. No such case was
-- found in the live data checked. If one turns up later, narrow this
-- migration's WHERE clause rather than widen the running code's guard.
--
-- Soft-deactivate (is_active = false) rather than DELETE, matching the
-- existing tombstone convention (markInactiveNodes / MarkStaleEdgesInactive)
-- so the rows remain available for audit/recovery. Idempotent: re-running
-- finds nothing left to update once applied.
UPDATE knowledge_graph_node n
SET is_active = false
WHERE n.source = 'traces'
  AND n.node_type IN ('MessageQueue', 'Database', 'Cache')
  AND n.is_active = true
  AND NOT EXISTS (
    SELECT 1
    FROM jsonb_array_elements_text(COALESCE(n.properties -> 'types', '[]'::jsonb)) AS t(value)
    WHERE n.properties ->> 'name' ILIKE '%' || t.value || '%'
  );

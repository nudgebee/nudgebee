-- Second follow-up to V815/V824 (nudgebee-enterprise#34880, #34899, #35091):
-- those fixes excluded CLIENT/PRODUCER/CONSUMER spans from
-- detectApplicationType's span-name fallback, but only when span.kind was
-- actually populated. Live data showed a real trace source (services-server's
-- rabbitmq.consume spans) never populates span.kind at all, so the fallback
-- kept trusting the span name and recreated the same phantom MessageQueue
-- nodes on every rebuild even after V824 deployed. The classifier now also
-- excludes operation-verb-suffixed span names (consume/process/receive/
-- send/produce/publish) when span.kind is absent, unless it's definitively
-- SERVER. This is the same one-time cleanup V815/V824 did, re-run to catch
-- phantom rows created between V824 and this fix landing.
--
-- Same discriminator as V815/V824: every OTHER classification path in
-- detectApplicationType requires the service's own name to contain the
-- matched technology keyword — only the span-name fallback can produce a
-- node whose properties.types keyword(s) are absent from its own
-- properties.name.
--
-- Soft-deactivate (is_active = false), matching the existing tombstone
-- convention. Idempotent: re-running finds nothing left to update once
-- applied; a no-op if V824 already caught everything.
UPDATE knowledge_graph_node n
SET is_active = false
WHERE n.source = 'traces'
  AND n.node_type IN ('MessageQueue', 'Database', 'Cache')
  AND n.is_active = true
  AND (
    jsonb_typeof(n.properties -> 'types') IS DISTINCT FROM 'array'
    OR NOT EXISTS (
      SELECT 1
      FROM jsonb_array_elements_text(n.properties -> 'types') AS t(value)
      WHERE n.properties ->> 'name' ILIKE '%' || t.value || '%'
    )
  );

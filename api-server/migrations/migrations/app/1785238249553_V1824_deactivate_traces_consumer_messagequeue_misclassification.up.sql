-- Follow-up to V815 (nudgebee-enterprise#34899): that migration's classifier
-- fix only excluded CLIENT/PRODUCER spans from detectApplicationType's
-- span-name fallback, deliberately leaving CONSUMER spans able to reclassify
-- the calling service (tested as a guardrail at the time). Live data showed
-- that assumption was wrong: an ordinary service that merely consumes from a
-- queue as one of several duties (e.g. services-server, which also serves
-- HTTP webhooks and calls Redis/Bedrock) got reclassified as a MessageQueue
-- because one of its many spans was named e.g. "rabbitmq.consume". The
-- classifier now excludes CONSUMER alongside CLIENT/PRODUCER; this is the
-- same one-time cleanup V815 did, re-run to catch phantom rows created
-- between V815 and this fix landing.
--
-- Same discriminator as V815: every OTHER classification path in
-- detectApplicationType requires the service's own name to contain the
-- matched technology keyword — only the span-name fallback can produce a
-- node whose properties.types keyword(s) are absent from its own
-- properties.name.
--
-- Soft-deactivate (is_active = false), matching the existing tombstone
-- convention. Idempotent: re-running finds nothing left to update once
-- applied; a no-op if V815 already caught everything.
--
-- properties -> 'types' is guarded with jsonb_typeof: jsonb_array_elements_text
-- errors at runtime if fed a non-array JSON value, and properties is a
-- loosely-typed JSONB blob written across code versions — a row where
-- 'types' isn't an array can't match this bug's discriminator anyway, so
-- treating "not an array" the same as "no match" is both safe and correct.
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

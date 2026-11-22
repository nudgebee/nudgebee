-- Restore the event id in alert-investigation conversation titles.
--
-- The Investigations tab and the investigation counts find an investigation's
-- event by the UUID in its conversation title. From 2026-10-19 step 1 of the
-- investigation ran through the plain LLM agent, whose conversations are titled
-- "Processing LLM Request", so new investigations dropped out of both. llm-server
-- now names these conversations itself; this renames the ones already created.
-- They need it: a fingerprint reuses its conversation on every later firing, so
-- the placeholder title would otherwise stay for good.
--
-- The event id is the first UUID in the conversation's first human message (the
-- step-1 brief prompt), kept only when that event's fingerprint matches the
-- conversation's session (the event id stands in for an empty fingerprint, as
-- the analyzer does). RCA-report sessions (event-rca-) are left alone: they
-- were always titled this way and must not count as a second investigation.
--
-- Lock window: one UPDATE over a few hundred rows; the filter scans
-- llm_conversations once, which takes seconds at a few hundred thousand rows.
WITH recovered AS (
    SELECT c.id,
           c.session_id,
           (SELECT substring(m.message FROM '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}')
              FROM llm_conversation_messages m
             WHERE m.conversation_id = c.id
               AND m.role = 'human'
               AND m.message_type = 'generation'
             ORDER BY m.created_at
             LIMIT 1) AS event_id
      FROM llm_conversations c
     WHERE c.source = 'Investigation'
       AND c.title = 'Processing LLM Request'
       AND c.session_id LIKE 'event-%'
       AND c.session_id NOT LIKE 'event-rca-%'
       AND c.created_at >= '2026-10-18'
)
UPDATE llm_conversations c
   SET title = 'Event details - ' || r.event_id
  FROM recovered r
  JOIN events e ON e.id = r.event_id::uuid
 WHERE c.id = r.id
   AND r.event_id IS NOT NULL
   AND r.session_id = 'event-' || COALESCE(NULLIF(e.fingerprint, ''), e.id::text);

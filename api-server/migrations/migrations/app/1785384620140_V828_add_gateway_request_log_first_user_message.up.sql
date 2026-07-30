-- Precompute a short preview of each captured request's opening user message so the AI
-- Gateway Sessions tab reads a cheap column instead of parsing the (large) body via
-- jsonb on every query. Populated at capture going forward; this backfills existing
-- rows. Raw — client wrapper tokens (e.g. <session>) are kept. Lives in the same
-- PHI-locked body table that already holds the full body, so no new exposure surface.

ALTER TABLE llm_gateway_request_log
  ADD COLUMN IF NOT EXISTS first_user_message text NOT NULL DEFAULT '';

-- PG15-safe validity guard (the `IS JSON` predicate is PG16+; prod/CI run PG15). A tiny
-- helper returns the parsed jsonb, or NULL for a truncated/invalid body, so a single bad
-- row can't abort the one-time backfill. Dropped once the backfill completes.
CREATE OR REPLACE FUNCTION nb_gw_backfill_try_jsonb(t text) RETURNS jsonb
  LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $fn$
BEGIN
  RETURN t::jsonb;
EXCEPTION WHEN others THEN
  RETURN NULL;
END
$fn$;

-- One-time backfill from existing bodies. jsonb_typeof guards array-vs-string content so
-- no row aborts the run. Whitespace-collapsed + 200-char cap (same as the capture path).
UPDATE llm_gateway_request_log rl
SET first_user_message = left(trim(regexp_replace(COALESCE(
    (SELECT COALESCE(
       CASE WHEN jsonb_typeof(fu.msg->'content') = 'array'
            THEN (SELECT string_agg(blk->>'text', ' ')
                    FROM jsonb_array_elements(fu.msg->'content') blk WHERE blk->>'type' = 'text') END,
       fu.msg->>'content')
     FROM jsonb_array_elements(
       CASE WHEN jsonb_typeof(nb_gw_backfill_try_jsonb(rl.request_body) -> 'messages') = 'array'
            THEN nb_gw_backfill_try_jsonb(rl.request_body) -> 'messages' ELSE '[]'::jsonb END
     ) WITH ORDINALITY AS fu(msg, ord)
     WHERE fu.msg->>'role' = 'user'
     ORDER BY fu.ord LIMIT 1),
    nb_gw_backfill_try_jsonb(rl.request_body) #>> '{contents,0,parts,0,text}',
    ''), '\s+', ' ', 'g')), 200)
WHERE first_user_message = '' AND nb_gw_backfill_try_jsonb(rl.request_body) IS NOT NULL;

DROP FUNCTION IF EXISTS nb_gw_backfill_try_jsonb(text);

-- Index the "earliest request per session" lookup the Sessions query does (LATERAL
-- ORDER BY created_at). Plain CREATE INDEX (CONCURRENTLY is rejected by CI — every
-- migration runs in a transaction); a brief write lock on this table is acceptable.
-- For a large prod table, pre-apply the CONCURRENTLY variant by hand before deploy.
CREATE INDEX IF NOT EXISTS idx_gateway_request_log_session_created
  ON llm_gateway_request_log (session_id, created_at);

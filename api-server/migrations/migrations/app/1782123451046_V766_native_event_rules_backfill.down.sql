-- Remove auto-registered native event-type rules (from the backfill and the
-- runtime forward-fill). They are identified by the empty alert-rule signature:
-- no expr and no annotation summary/description. Real prometheus/webhook/user
-- rules always carry an expr or an annotation summary, so they are spared.
-- Source lookup values are left in place (harmless once unreferenced).
DELETE FROM "public"."event_rules"
WHERE "expr" = ''
  AND COALESCE("annotations"->>'summary', '') = ''
  AND COALESCE("annotations"->>'description', '') = '';

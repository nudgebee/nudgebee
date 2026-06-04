-- Adds the canonical 'EVENT_AUTO_AI_SUMMARY' row to the feature lookup table
-- so that feature_flag rows referencing this value satisfy the FK to
-- feature(value). The runtime constant in api-server is
-- tenant.FEATURE_EVENT_AUTO_AI_SUMMARY = "EVENT_AUTO_AI_SUMMARY" (no prefix).
--
-- The original V578 migration (1760078786130_V578_insert_into_public_feature_auto_ai_summary)
-- inserted 'FEATURE_EVENT_AUTO_AI_SUMMARY' (with prefix) by mistake — that row
-- is orphaned and harmless to leave in place; this migration adds the row the
-- code path actually queries against. Idempotent.
INSERT INTO "public"."feature" ("value", "description")
VALUES ('EVENT_AUTO_AI_SUMMARY', 'Master kill switch for event auto-analysis (tenant or account scope)')
ON CONFLICT ("value") DO NOTHING;

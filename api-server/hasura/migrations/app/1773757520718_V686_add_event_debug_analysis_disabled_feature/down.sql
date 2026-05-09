INSERT INTO "public"."feature"("description", "value")
VALUES ('Enable debug analysis for events at account level', 'EVENT_DEBUG_ANALYSIS_ENABLED')
ON CONFLICT (value) DO NOTHING;

DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'EVENT_DEBUG_ANALYSIS_DISABLED';
DELETE FROM "public"."feature" WHERE "value" = 'EVENT_DEBUG_ANALYSIS_DISABLED';

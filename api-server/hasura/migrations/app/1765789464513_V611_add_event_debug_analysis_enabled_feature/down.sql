
DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'EVENT_DEBUG_ANALYSIS_ENABLED';
DELETE FROM "public"."feature" WHERE "value" = 'EVENT_DEBUG_ANALYSIS_ENABLED';

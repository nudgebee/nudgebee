-- Remove any enablement rows first (FK), then the feature registration.
DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'AI_COST_REPORT';
DELETE FROM "public"."feature" WHERE "value" = 'AI_COST_REPORT';

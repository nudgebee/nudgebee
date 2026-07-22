-- Remove any enablement rows first (FK), then the feature registration.
DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'EVENT_AUTO_RAISE_PR_ENABLED';
DELETE FROM "public"."feature" WHERE "value" = 'EVENT_AUTO_RAISE_PR_ENABLED';

DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'SERVER_ORCHESTRATED_SCANNERS';
DELETE FROM "public"."feature" WHERE "value" = 'SERVER_ORCHESTRATED_SCANNERS';

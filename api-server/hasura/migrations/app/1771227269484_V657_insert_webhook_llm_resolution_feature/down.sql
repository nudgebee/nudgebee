DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'WEBHOOK_LLM_RESOLUTION';
DELETE FROM "public"."feature" WHERE "value" = 'WEBHOOK_LLM_RESOLUTION';

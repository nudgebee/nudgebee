-- Drops the orphan LLM_CODE_ANALYSIS_RAISE_PR feature (added V574). No code has
-- ever read it: raise-PR is driven by the request's raise_pr field, and the
-- separate EVENT_AUTO_RAISE_PR_ENABLED feature covers the event-analysis path.
--
-- feature_flag.feature_flag_feature_fkey is ON DELETE RESTRICT, so the tenant
-- flag rows must go first or the DELETE below fails.
DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'LLM_CODE_ANALYSIS_RAISE_PR';
DELETE FROM "public"."billing_feature_mapping" WHERE "feature_id" = 'LLM_CODE_ANALYSIS_RAISE_PR';
DELETE FROM "public"."billing_plan_features" WHERE "feature_id" = 'LLM_CODE_ANALYSIS_RAISE_PR';
DELETE FROM "public"."feature" WHERE "value" = 'LLM_CODE_ANALYSIS_RAISE_PR';

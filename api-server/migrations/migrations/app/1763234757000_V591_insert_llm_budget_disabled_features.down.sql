-- Remove LLM budget disabled feature flags from feature table
DELETE FROM "public"."feature" WHERE "value" = 'LLM_BUDGET_DISABLED_INVESTIGATION';
DELETE FROM "public"."feature" WHERE "value" = 'LLM_BUDGET_DISABLED_USER_INVESTIGATION';

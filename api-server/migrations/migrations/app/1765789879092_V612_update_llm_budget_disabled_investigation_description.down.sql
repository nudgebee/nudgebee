
UPDATE "public"."feature"
SET "description" = 'Disable LLM budget checks for investigation module'
WHERE "value" = 'LLM_BUDGET_DISABLED_INVESTIGATION';

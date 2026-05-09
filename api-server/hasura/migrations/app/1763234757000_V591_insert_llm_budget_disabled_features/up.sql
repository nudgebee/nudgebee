-- Add LLM budget disabled feature flags to feature table
INSERT INTO "public"."feature" ("value", "description")
VALUES ('LLM_BUDGET_DISABLED_INVESTIGATION', 'Disable LLM budget checks for investigation module')
ON CONFLICT ("value") DO NOTHING;

INSERT INTO "public"."feature" ("value", "description")
VALUES ('LLM_BUDGET_DISABLED_USER_INVESTIGATION', 'Disable LLM budget checks for user investigation module')
ON CONFLICT ("value") DO NOTHING;

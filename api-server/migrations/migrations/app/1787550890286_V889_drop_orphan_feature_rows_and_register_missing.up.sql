-- Feature-registry reconciliation for api-server (#34892). Pure DML against the
-- `feature` lookup table and its dependants. Three groups:
--
-- 1. Drop rows nothing reads. Verified 0 readers repo-wide (Go/TS/Python/SQL/YAML):
--    * FEATURE_EVENT_AUTO_AI_SUMMARY — the V578 typo row. The Go CONSTANT is named
--      tenant.FEATURE_EVENT_AUTO_AI_SUMMARY but its VALUE is the unprefixed
--      'EVENT_AUTO_AI_SUMMARY', which V745 added as the canonical row; V745's own
--      comment already calls this one a mistake. Nothing passes the prefixed string.
--    * LLM_BUDGET_DISABLED_INVESTIGATION / _USER_INVESTIGATION — superseded, not
--      merely abandoned: V693 copied these rows into llm_budget_config.budget_disabled
--      (see its budget_disabled backfill) and llm-server's budget/service.go:78,138
--      reads only that column now.
--    * TICKETS_ADD_EVENT_COMMENTS — seeded V583, never wired to a reader. Its Go
--      constant is removed in the same change.
--    * NEW_CONTEXT / NEW_LLM_TOOLS / REMEDIATION_SHOW_PLAN — pre-migration-era rows:
--      no migration in the tree ever seeded them and the strings appear in no source
--      file. Deleted unconditionally so this is correct on every environment,
--      including on-prem installs that may never have had them.
--
-- 2. Register two kill switches that HAVE readers but were never seeded.
--    feature_flag.feature_id FKs to feature(value), so with no parent row the
--    'disabled' row these readers look for cannot be inserted — both switches are
--    permanently ON and un-toggleable. Inserting the parent changes no behaviour;
--    it only makes the documented override possible.
--
-- 3. Backfill the missing ANOMALY_DETECTION description.
--
-- feature_flag.feature_flag_feature_fkey is ON DELETE RESTRICT and the billing_*
-- FKs are NO ACTION, so every dependant must go before the parent row or the final
-- DELETE fails. Every statement is idempotent.

-- 1. Orphan rows -------------------------------------------------------------

DELETE FROM "public"."feature_flag" WHERE "feature_id" IN (
    'FEATURE_EVENT_AUTO_AI_SUMMARY',
    'LLM_BUDGET_DISABLED_INVESTIGATION',
    'LLM_BUDGET_DISABLED_USER_INVESTIGATION',
    'TICKETS_ADD_EVENT_COMMENTS',
    'NEW_CONTEXT',
    'NEW_LLM_TOOLS',
    'REMEDIATION_SHOW_PLAN'
);

DELETE FROM "public"."billing_feature_mapping" WHERE "feature_id" IN (
    'FEATURE_EVENT_AUTO_AI_SUMMARY',
    'LLM_BUDGET_DISABLED_INVESTIGATION',
    'LLM_BUDGET_DISABLED_USER_INVESTIGATION',
    'TICKETS_ADD_EVENT_COMMENTS',
    'NEW_CONTEXT',
    'NEW_LLM_TOOLS',
    'REMEDIATION_SHOW_PLAN'
);

DELETE FROM "public"."billing_plan_features" WHERE "feature_id" IN (
    'FEATURE_EVENT_AUTO_AI_SUMMARY',
    'LLM_BUDGET_DISABLED_INVESTIGATION',
    'LLM_BUDGET_DISABLED_USER_INVESTIGATION',
    'TICKETS_ADD_EVENT_COMMENTS',
    'NEW_CONTEXT',
    'NEW_LLM_TOOLS',
    'REMEDIATION_SHOW_PLAN'
);

DELETE FROM "public"."feature" WHERE "value" IN (
    'FEATURE_EVENT_AUTO_AI_SUMMARY',
    'LLM_BUDGET_DISABLED_INVESTIGATION',
    'LLM_BUDGET_DISABLED_USER_INVESTIGATION',
    'TICKETS_ADD_EVENT_COMMENTS',
    'NEW_CONTEXT',
    'NEW_LLM_TOOLS',
    'REMEDIATION_SHOW_PLAN'
);

-- 2. Missing lookup rows for features that are already read -------------------

-- Read by anomoly/service.go:324 and spend/opencost_sync.go:61 via
-- tenant.IsFeatureEnabledByDefault (default-ON), but never registered.
INSERT INTO "public"."feature"("value", "description") VALUES
    ('ANOMALY_DETECTION_ERROR_RATE', 'Error-rate metric anomaly detection. Default ON; set this flag to ''disabled'' for a tenant to suppress error-rate anomalies while leaving the other detectors on.'),
    ('OPENCOST_SERVER_SIDE_SPEND', 'Default-ON kill switch for the server-side OpenCost spend-sync cron. Enrolment is automatic for connected K8s clusters whose agent has its own OpenCost disabled; set this flag to ''disabled'' to force a tenant''s clusters off the server-side path.')
ON CONFLICT ("value") DO NOTHING;

-- 3. Description backfill -----------------------------------------------------

-- ANOMALY_DETECTION shipped without a description. Its readers
-- (anomoly/service.go:150,174,376 and anomoly/spend_anomaly.go:244) use
-- IsFeatureEnabled, i.e. opt-in / default-OFF — worth stating explicitly, since
-- most flags in this table are default-ON. Guarded so a description added by any
-- other route is not clobbered.
UPDATE "public"."feature"
SET "description" = 'Metric and spend anomaly detection (CPU, memory, latency, replicas, error rate, cloud spend). Opt-in: a tenant sees anomalies only with an ''enabled'' feature_flag row.'
WHERE "value" = 'ANOMALY_DETECTION' AND "description" IS NULL;

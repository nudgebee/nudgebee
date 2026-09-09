-- Reverses V889. Restores the deleted `feature` rows only: the tenant/account
-- feature_flag rows are deliberately NOT recreated, since nothing read them and
-- their loss is not a behaviour change to roll back — and their per-tenant values
-- are not recoverable from here. Same reasoning as V883.
--
-- The descriptions below are the live values at the time of writing, not
-- reconstructions from the seeding migrations: V612 rewrote
-- LLM_BUDGET_DISABLED_INVESTIGATION's description after V591 seeded it.

INSERT INTO "public"."feature"("value", "description") VALUES
    ('FEATURE_EVENT_AUTO_AI_SUMMARY',          'Automatically generate AI summary for events'),
    ('LLM_BUDGET_DISABLED_INVESTIGATION',      'Disable LLM budget checks for event investigation module'),
    ('LLM_BUDGET_DISABLED_USER_INVESTIGATION', 'Disable LLM budget checks for user investigation module'),
    ('TICKETS_ADD_EVENT_COMMENTS',             'Add comments on tickets for event evidence report'),
    ('NEW_CONTEXT',                            'NEW_CONTEXT'),
    ('NEW_LLM_TOOLS',                          'New llm tools tab'),
    ('REMEDIATION_SHOW_PLAN',                  'Show remediation plan in LLM responses')
ON CONFLICT ("value") DO NOTHING;

-- Un-register the two kill switches. Any feature_flag row created against them
-- while V889 was applied must go first (feature_flag's FK is ON DELETE RESTRICT).
DELETE FROM "public"."feature_flag" WHERE "feature_id" IN ('ANOMALY_DETECTION_ERROR_RATE', 'OPENCOST_SERVER_SIDE_SPEND');
DELETE FROM "public"."feature" WHERE "value" IN ('ANOMALY_DETECTION_ERROR_RATE', 'OPENCOST_SERVER_SIDE_SPEND');

-- The ANOMALY_DETECTION description backfill is deliberately not reverted:
-- restoring a NULL description carries no behaviour and would only re-open the
-- documentation gap V889 closed.

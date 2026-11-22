DELETE FROM notification_rules WHERE source = 'llm_budget';
DELETE FROM "public"."notification_source_type" WHERE "value" = 'llm_budget';
ALTER TABLE llm_gateway_rate_limits DROP CONSTRAINT IF EXISTS llm_gateway_rate_limits_soft_pct_check;
ALTER TABLE llm_gateway_rate_limits
    DROP COLUMN IF EXISTS soft_pct,
    DROP COLUMN IF EXISTS alerted_window,
    DROP COLUMN IF EXISTS alerted_pct;

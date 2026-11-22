-- #41080/#41078: a quota's warn level (percent of its limit; never blocks) and the last
-- budget alert sent for it (window start + threshold), so each threshold alerts once per
-- window. Nullable, no default: metadata-only on Postgres 11+, no table rewrite.
ALTER TABLE llm_gateway_rate_limits
    ADD COLUMN IF NOT EXISTS soft_pct smallint,
    ADD COLUMN IF NOT EXISTS alerted_window timestamptz,
    ADD COLUMN IF NOT EXISTS alerted_pct smallint;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'llm_gateway_rate_limits_soft_pct_check'
                   AND conrelid = 'llm_gateway_rate_limits'::regclass) THEN
        ALTER TABLE llm_gateway_rate_limits
            ADD CONSTRAINT llm_gateway_rate_limits_soft_pct_check
            CHECK (soft_pct IS NULL OR soft_pct BETWEEN 1 AND 99) NOT VALID;
    END IF;
END $$;

-- Rule source for the budget alert channel post (notification_rules.source FK).
INSERT INTO "public"."notification_source_type"("value") VALUES ('llm_budget') ON CONFLICT DO NOTHING;

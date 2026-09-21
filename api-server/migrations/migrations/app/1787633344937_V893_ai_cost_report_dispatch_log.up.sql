-- Idempotency guard for the AI Cost Daily Report's hourly dispatch cron —
-- an INSERT ... ON CONFLICT DO NOTHING against the unique key below caps a
-- tenant at one report per report_date even on a duplicate cron fire.
CREATE TABLE IF NOT EXISTS ai_cost_report_dispatch_log (
    id            uuid NOT NULL DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    report_date   date NOT NULL,
    dispatched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (tenant_id, report_date)
);

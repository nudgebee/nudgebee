-- Multi-window burn-rate SLO evaluation.
--
-- slo_report.status previously only had OK / FIRING, so a report the agent
-- marked invalid (no data, too few events, SLI out of range) was written as OK.
-- An untrafficked workload therefore read as healthy and inflated the 30-day
-- attainment aggregate. NO_DATA separates "could not measure" from "measured
-- and healthy".
--
-- Ordering note: slo_status must carry NO_DATA before the api-server that
-- writes it rolls out, or slo_report_status_fkey rejects the insert.
INSERT INTO "public"."slo_status" ("value") VALUES ('NO_DATA')
    ON CONFLICT ("value") DO NOTHING;

-- severity is the worst burn-rate severity across the alert rules (OK /
-- WARNING / CRITICAL); burn_rates is the per-rule vector, each entry carrying
-- both windows' burn rate and percentage plus the threshold it was judged
-- against. Both stay NULL for reports produced by agents that predate the
-- multi-window evaluation.
ALTER TABLE "public"."slo_report" ADD COLUMN IF NOT EXISTS "severity" text;
ALTER TABLE "public"."slo_report" ADD COLUMN IF NOT EXISTS "burn_rates" jsonb;

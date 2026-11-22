-- CPU SLOs with breach forecasting (docs/cpu-throttling-forecast-spec.md). No feature
-- flag: a CPU SLO is opted into per workload, and nb_forecast_providers="" is the
-- server-level kill switch.
-- Idempotent; no lock beyond a catalog-only ALTER TABLE on slo_config.

-- 1. The source of the CPUThrottlingPredicted / CPUSaturationPredicted events the
--    forecast raises. event_rule_source too, so the native event-type registration
--    (event/native_event_rules.go) can store them and users can pick them as an
--    automation trigger.
INSERT INTO "public"."event_source" ("value") VALUES ('forecast') ON CONFLICT ("value") DO NOTHING;
INSERT INTO "public"."event_rule_source" ("value") VALUES ('forecast') ON CONFLICT ("value") DO NOTHING;

-- 2. The latest forecast of each CPU SLO, which the SLO list shows without a live
--    call. NULL for every other SLO type, so existing rows need no backfill.
ALTER TABLE "public"."slo_config" ADD COLUMN IF NOT EXISTS "last_forecast" jsonb;

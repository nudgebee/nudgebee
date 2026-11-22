ALTER TABLE "public"."slo_config" DROP COLUMN IF EXISTS "last_forecast";

-- event_source / event_rule_source rows are kept: events and rules written with
-- source 'forecast' reference them.

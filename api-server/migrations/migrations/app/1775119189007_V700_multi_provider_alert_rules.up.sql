-- V700: Multi-provider alert rule creation
-- Extends event_rules to support creating alerts in any external observability system

-- 1. alert_type enum table (metric or log alerts)
CREATE TABLE "public"."event_rule_alert_type" (
  "value" text NOT NULL, PRIMARY KEY ("value")
);
INSERT INTO "public"."event_rule_alert_type"("value") VALUES ('metric'), ('log');

-- 2. Add alert_type column to event_rules (default 'metric' for backward compat)
ALTER TABLE "public"."event_rules"
  ADD COLUMN "alert_type" text NOT NULL DEFAULT 'metric'
  REFERENCES "public"."event_rule_alert_type"("value")
  ON UPDATE RESTRICT ON DELETE RESTRICT;

-- 3. Add provider columns (which observability provider to route to)
ALTER TABLE "public"."event_rules"
  ADD COLUMN "metric_provider" text NULL,
  ADD COLUMN "metric_provider_source" text NULL;

-- 4. External rule ID (to update/delete in the external system)
ALTER TABLE "public"."event_rules"
  ADD COLUMN "external_rule_id" text NULL;

-- 5. Provider-specific config (thresholds, evaluation periods, etc.)
ALTER TABLE "public"."event_rules"
  ADD COLUMN "provider_config" jsonb NULL;

-- 6. New event_rule_source values for proactive rule creation
--    (separate from _webhook sources which are for ingested alerts)
INSERT INTO "public"."event_rule_source"("value") VALUES
  ('datadog'), ('newrelic'), ('dynatrace'), ('cloudwatch'),
  ('azure_monitor'), ('gcp_monitoring'), ('splunk'), ('elasticsearch'),
  ('loki'), ('signoz'), ('grafana'), ('chronosphere_user')
ON CONFLICT DO NOTHING;

-- 7. Add alert_type to agent_playbook_action for action filtering
ALTER TABLE "public"."agent_playbook_action"
  ADD COLUMN "alert_type" text NULL;


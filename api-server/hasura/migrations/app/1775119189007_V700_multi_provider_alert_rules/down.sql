-- V700: Rollback multi-provider alert rule creation

ALTER TABLE "public"."event_rules" DROP COLUMN IF EXISTS "alert_type";
ALTER TABLE "public"."event_rules" DROP COLUMN IF EXISTS "metric_provider";
ALTER TABLE "public"."event_rules" DROP COLUMN IF EXISTS "metric_provider_source";
ALTER TABLE "public"."event_rules" DROP COLUMN IF EXISTS "external_rule_id";
ALTER TABLE "public"."event_rules" DROP COLUMN IF EXISTS "provider_config";

DROP TABLE IF EXISTS "public"."event_rule_alert_type";

ALTER TABLE "public"."agent_playbook_action" DROP COLUMN IF EXISTS "alert_type";

DELETE FROM "public"."event_rule_source" WHERE "value" IN (
  'datadog', 'newrelic', 'dynatrace', 'cloudwatch',
  'azure_monitor', 'gcp_monitoring', 'splunk', 'elasticsearch',
  'loki', 'signoz', 'grafana', 'chronosphere_user'
);

DELETE FROM "public"."event_rule_source" WHERE "value" = 'gcp_monitoring_webhook';
DELETE FROM "public"."event_source" WHERE "value" = 'gcp_monitoring_webhook';

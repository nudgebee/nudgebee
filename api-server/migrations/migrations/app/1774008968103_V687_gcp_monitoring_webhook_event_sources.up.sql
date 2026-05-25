INSERT INTO "public"."event_source"("value") VALUES ('gcp_monitoring_webhook')
ON CONFLICT DO NOTHING;

INSERT INTO "public"."event_rule_source"("value") VALUES ('gcp_monitoring_webhook')
ON CONFLICT DO NOTHING;

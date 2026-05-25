
DELETE FROM "public"."event_source" WHERE "value" = 'azure_monitor_webhook';

DELETE FROM "public"."event_rule_source" WHERE "value" = 'azure_monitor_webhook';

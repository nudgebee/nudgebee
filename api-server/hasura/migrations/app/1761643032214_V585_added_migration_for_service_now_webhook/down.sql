
DELETE FROM "public"."integration_types" WHERE "name" = 'servicenow_webhook';

DELETE FROM "public"."event_source" WHERE "value" = 'servicenow_webhook';

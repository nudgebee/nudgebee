
INSERT INTO "public"."event_source"("value") VALUES (E'servicenow_webhook');

INSERT INTO "public"."integration_types"("category", "description", "name") VALUES (E'incident_webhook', null, E'servicenow_webhook');

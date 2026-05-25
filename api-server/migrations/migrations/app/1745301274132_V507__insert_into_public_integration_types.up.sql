
INSERT INTO "public"."integration_types"("category", "description", "name") VALUES (E'incident_webhook', null, E'prometheus_alertmanager_webhook') ON CONFLICT ("name") DO NOTHING;

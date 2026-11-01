-- Register the SigNoz incident webhook, so it can be created from Admin -> Integrations,
-- and accept it as the source of the events it parses and the event rules it upserts.
-- 'signoz' (rules Nudgebee creates IN SigNoz) is already an event_rule_source (V700).
INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('signoz_webhook', 'incident_webhook', 'SigNoz alert notifications')
ON CONFLICT ("name") DO NOTHING;

INSERT INTO event_source(value) VALUES ('signoz_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('signoz_webhook') ON CONFLICT DO NOTHING;

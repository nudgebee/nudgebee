-- Register incidentio_webhook as an incident webhook integration type so it can
-- be created from Admin → Integrations → Webhooks. Mirrors the openobserve_webhook
-- registration in V851: the outbound ticketing integration (incidentio, V1829) and
-- the inbound webhook source are separate integration rows with separate categories.
INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('incidentio_webhook', 'incident_webhook', 'incident.io incident notifications')
ON CONFLICT (name) DO NOTHING;

-- Register incidentio_webhook as a valid event source and event rule source so
-- events parsed from the webhook, and the event rules it upserts, both validate
-- against the event_rules.source foreign key.
INSERT INTO event_source(value) VALUES ('incidentio_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('incidentio_webhook') ON CONFLICT DO NOTHING;

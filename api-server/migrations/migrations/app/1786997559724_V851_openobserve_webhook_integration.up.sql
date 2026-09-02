-- Register openobserve_webhook as an incident webhook integration type so it
-- can be created from Admin → Integrations → Webhooks.
INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('openobserve_webhook', 'incident_webhook', 'OpenObserve alert notifications')
ON CONFLICT (name) DO NOTHING;

-- Register openobserve_webhook as a valid event source and event rule source so
-- events parsed from the webhook, and the event rules it upserts, both validate.
INSERT INTO event_source(value) VALUES ('openobserve_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('openobserve_webhook') ON CONFLICT DO NOTHING;

-- Register the event webhook (#39586): one documented JSON contract any
-- tool can post alerts to, without a per-tool integration.
INSERT INTO integration_types(name, category, description) VALUES
  ('event_webhook', 'incident_webhook', 'Event Webhook')
ON CONFLICT (name) DO NOTHING;

INSERT INTO event_source(value) VALUES ('event_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('event_webhook') ON CONFLICT DO NOTHING;

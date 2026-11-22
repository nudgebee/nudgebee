-- Register the SolarWinds Platform (Orion) webhook (#41868): Orion alert actions post one
-- fixed body per alert; events and rules are stored under source orion_webhook.
INSERT INTO integration_types(name, category, description) VALUES
  ('orion_webhook', 'incident_webhook', 'SolarWinds Platform (Orion) Webhook')
ON CONFLICT (name) DO NOTHING;

INSERT INTO event_source(value) VALUES ('orion_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('orion_webhook') ON CONFLICT DO NOTHING;

-- Register SolarWinds Observability webhook as an incident_webhook integration type
INSERT INTO integration_types(name, category, description) VALUES
  ('solarwinds_webhook', 'incident_webhook', 'SolarWinds Observability Alert Webhook')
ON CONFLICT (name) DO NOTHING;

-- Register solarwinds_webhook as a valid event source and event rule source
INSERT INTO event_source(value) VALUES ('solarwinds_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('solarwinds_webhook') ON CONFLICT DO NOTHING;

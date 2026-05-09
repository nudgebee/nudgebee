-- Register Dynatrace webhook as an incident_webhook integration type
INSERT INTO integration_types(name, category, description) VALUES
  ('dynatrace_webhook', 'incident_webhook', 'Dynatrace Problem Notification Webhook')
ON CONFLICT (name) DO NOTHING;

-- Register dynatrace_webhook as a valid event source
INSERT INTO event_source(value) VALUES ('dynatrace_webhook') ON CONFLICT DO NOTHING;

-- Add Splunk integration types
INSERT INTO integration_types(name, category, description) VALUES
  ('splunk', 'observability_platform', 'Splunk Observability Platform'),
  ('splunk_webhook', 'incident_webhook', 'Splunk Alert Webhook')
ON CONFLICT (name) DO NOTHING;

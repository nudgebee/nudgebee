-- Add New Relic integration types
INSERT INTO integration_types(name, category, description) VALUES
  ('newrelic', 'observability_platform', 'New Relic Observability Platform'),
  ('newrelic_webhook', 'incident_webhook', 'New Relic Alert Webhook'),
  ('gitlab', '', 'GitLab Integration')
ON CONFLICT (name) DO NOTHING;

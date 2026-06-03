-- Add Dynatrace integration type
INSERT INTO integration_types(name, category, description) VALUES
  ('dynatrace', 'observability_platform', 'Dynatrace Observability Platform')
ON CONFLICT (name) DO NOTHING;

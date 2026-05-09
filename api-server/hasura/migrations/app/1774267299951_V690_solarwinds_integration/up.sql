-- Add SolarWinds Observability integration type
INSERT INTO integration_types(name, category, description) VALUES
  ('solarwinds', 'observability_platform', 'SolarWinds Observability Platform')
ON CONFLICT (name) DO NOTHING;

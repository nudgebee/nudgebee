-- Add Jaeger integration type to integration_types
INSERT INTO integration_types (name, category, description) VALUES
  ('jaeger', 'observability_platform', 'Jaeger distributed tracing')
ON CONFLICT (name) DO NOTHING;

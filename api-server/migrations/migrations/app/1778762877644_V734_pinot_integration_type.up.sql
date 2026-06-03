-- Register Apache Pinot as a log integration type
INSERT INTO integration_types(name, category, description) VALUES
  ('pinot', 'log', 'Apache Pinot')
ON CONFLICT (name) DO NOTHING;

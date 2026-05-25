-- Register Apache Hive as a log integration type
INSERT INTO integration_types(name, category, description) VALUES
  ('hive', 'log', 'Apache Hive')
ON CONFLICT (name) DO NOTHING;

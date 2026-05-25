INSERT INTO integration_types(name, category, description) VALUES
  ('vm_agent', 'proxy', 'Proxy Agent (Forager)'),
  ('mssql', 'database', 'Microsoft SQL Server'),
  ('oracle', 'database', 'Oracle Database')
ON CONFLICT (name) DO NOTHING;

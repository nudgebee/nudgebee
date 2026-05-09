-- Rename Splunk integration from 'splunk' (Splunk Platform/SPL) to
-- 'splunk_observability_platform' (Splunk Observability Cloud / formerly SignalFx)

-- Insert new integration type
INSERT INTO integration_types(name, category, description) VALUES
  ('splunk_observability_platform', 'observability_platform', 'Splunk Observability Cloud')
ON CONFLICT (name) DO NOTHING;

-- Remove old integration type (safe after migrating all references above)
DELETE FROM integration_types WHERE name = 'splunk';

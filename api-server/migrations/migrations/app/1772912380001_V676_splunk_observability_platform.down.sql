-- Reverse: rename 'splunk_observability_platform' back to 'splunk'

INSERT INTO integration_types(name, category, description) VALUES
  ('splunk', 'observability_platform', 'Splunk Observability Platform')
ON CONFLICT (name) DO NOTHING;

UPDATE integrations
  SET integration_type = 'splunk'
  WHERE integration_type = 'splunk_observability_platform';

UPDATE cloud_account_configs
  SET value = 'splunk'
  WHERE name IN ('log_provider', 'metric_provider') AND value = 'splunk_observability_platform';

DELETE FROM integration_types WHERE name = 'splunk_observability_platform';

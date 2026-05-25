-- Rollback migration: Restore tickets table and delete migrated data

-- Step 1: Restore tickets table foreign key
ALTER TABLE tickets DROP CONSTRAINT IF EXISTS tickets_integration_id_fkey;

ALTER TABLE tickets ADD CONSTRAINT tickets_configuration_id_fkey
  FOREIGN KEY (configuration_id) REFERENCES jira_configurations(id) ON DELETE NO ACTION;

ALTER TABLE tickets RENAME COLUMN integration_id TO configuration_id;

-- Step 2: Delete integration_config_values for migrated ticketing integrations
DELETE FROM integration_config_values
WHERE integration_id IN (
  SELECT id FROM integrations WHERE type IN ('jira', 'github_issues', 'servicenow', 'pagerduty')
);

-- Step 3: Delete integrations for ticketing types
DELETE FROM integrations WHERE type IN ('jira', 'github_issues', 'servicenow', 'pagerduty');

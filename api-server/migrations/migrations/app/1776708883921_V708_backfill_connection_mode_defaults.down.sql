-- Remove backfilled connection_mode rows for mssql/oracle integrations
-- that were added by the up migration.
DELETE FROM integration_config_values
WHERE name = 'connection_mode'
  AND value = 'vm_agent'
  AND integration_id IN (
      SELECT i.id FROM integrations i
      WHERE i.type IN ('mssql', 'oracle')
        AND i.source = 'user'
        AND NOT EXISTS (
            SELECT 1 FROM integration_config_values icv
            WHERE icv.integration_id = i.id AND icv.name = 'k8s_secret'
        )
  );

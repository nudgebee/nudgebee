-- Backfill connection_mode=vm_agent for user-created DB integrations that are
-- missing it.  These integrations were created via the UI where connection_mode
-- is hidden with a default of vm_agent, but the default was never persisted.
-- Without this value, the runtime check isVMAgentModeIntegration() returns false
-- and mis-routes queries to a pod-based path (e.g. sqlcmd not found).
--
-- Only targets integrations that have NO k8s_secret (newer cloud_push style)
-- and NO existing connection_mode row.

INSERT INTO integration_config_values (id, integration_id, name, value, is_encrypted, created_at, updated_at)
SELECT
    gen_random_uuid(),
    i.id,
    'connection_mode',
    'vm_agent',
    false,
    now(),
    now()
FROM integrations i
WHERE i.type IN ('mssql', 'oracle')
  AND i.source = 'user'
  AND NOT EXISTS (
      SELECT 1 FROM integration_config_values icv
      WHERE icv.integration_id = i.id AND icv.name = 'connection_mode'
  )
  AND NOT EXISTS (
      SELECT 1 FROM integration_config_values icv
      WHERE icv.integration_id = i.id AND icv.name = 'k8s_secret'
  );

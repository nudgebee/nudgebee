-- Revert: rename mcp back to mcp_proxy and remove from integration_types
UPDATE integrations SET type = 'mcp_proxy' WHERE type = 'mcp';

UPDATE integration_config_values
SET value = 'mcp_proxy'
WHERE name = 'proxy_type' AND value = 'mcp'
  AND integration_id IN (SELECT id FROM integrations WHERE type = 'mcp_proxy');

DELETE FROM integration_types WHERE name = 'mcp';

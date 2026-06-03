-- Revert: rename mcp back to mcp_proxy
UPDATE integrations SET type = 'mcp_proxy' WHERE type = 'mcp';

-- Revert config values that reference the type name
UPDATE integration_config_values
SET value = 'mcp_proxy'
WHERE name = 'proxy_type' AND value = 'mcp'
  AND integration_id IN (SELECT id FROM integrations WHERE type = 'mcp_proxy');

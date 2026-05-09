-- Rename integration type from mcp_proxy to mcp.
-- MCP integrations now support both direct and vm_agent connection modes,
-- so the "_proxy" suffix is misleading.
UPDATE integrations SET type = 'mcp' WHERE type = 'mcp_proxy';

-- Update any config values that reference the old type name
UPDATE integration_config_values
SET value = 'mcp'
WHERE name = 'proxy_type' AND value = 'mcp_proxy'
  AND integration_id IN (SELECT id FROM integrations WHERE type = 'mcp');

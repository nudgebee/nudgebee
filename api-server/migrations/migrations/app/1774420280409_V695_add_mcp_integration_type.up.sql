-- Add 'mcp' to integration_types (required before integrations.type can reference it).
-- V692 renamed integrations.type from mcp_proxy to mcp but missed adding
-- the new type to integration_types, causing FK constraint violations.
INSERT INTO integration_types(name, category, description)
VALUES ('mcp', 'proxy', 'MCP (Model Context Protocol) server integration')
ON CONFLICT (name) DO NOTHING;

-- Re-run the rename in case V692 failed due to the missing FK target.
UPDATE integrations SET type = 'mcp' WHERE type = 'mcp_proxy';

UPDATE integration_config_values
SET value = 'mcp'
WHERE name = 'proxy_type' AND value = 'mcp_proxy'
  AND integration_id IN (SELECT id FROM integrations WHERE type = 'mcp');

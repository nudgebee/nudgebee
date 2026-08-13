-- Add freshdesk ticketing integration type (FK target for integrations.type)
INSERT INTO integration_types(name, category, description)
VALUES ('freshdesk', 'ticketing', 'Freshdesk helpdesk ticketing')
ON CONFLICT(name) DO NOTHING;

-- Add freshdesk to ticket tool types (for tickets.platform/tool foreign key)
INSERT INTO "public"."ticket_tool_types"("value") VALUES ('freshdesk')
ON CONFLICT DO NOTHING;

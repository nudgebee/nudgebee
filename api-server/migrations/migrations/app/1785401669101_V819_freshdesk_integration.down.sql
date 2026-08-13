-- Remove freshdesk from ticket tool types
DELETE FROM "public"."ticket_tool_types" WHERE "value" = 'freshdesk';

-- Remove freshdesk ticketing integration type
DELETE FROM integration_types WHERE name = 'freshdesk';

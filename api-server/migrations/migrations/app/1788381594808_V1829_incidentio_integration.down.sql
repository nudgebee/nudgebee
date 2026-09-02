-- Remove incident.io from ticket tool types
DELETE FROM "public"."ticket_tool_types" WHERE "value" = 'incidentio';

-- Remove incident.io ticketing integration type
DELETE FROM integration_types WHERE name = 'incidentio';

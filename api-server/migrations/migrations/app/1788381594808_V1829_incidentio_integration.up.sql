-- Add incident.io ticketing integration type
INSERT INTO integration_types(name, category, description)
VALUES('incidentio', 'ticketing', 'incident.io incident management')
ON CONFLICT(name) DO NOTHING;

-- Add incidentio to ticket tool types (for tickets.platform foreign key)
INSERT INTO "public"."ticket_tool_types"("value") VALUES ('incidentio')
ON CONFLICT DO NOTHING;

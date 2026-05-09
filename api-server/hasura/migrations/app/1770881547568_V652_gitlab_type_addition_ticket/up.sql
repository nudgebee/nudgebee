-- Add zenduty to ticket tool types (for tickets.platform foreign key)
INSERT INTO "public"."ticket_tool_types"("value") VALUES ('gitlab')
ON CONFLICT DO NOTHING;
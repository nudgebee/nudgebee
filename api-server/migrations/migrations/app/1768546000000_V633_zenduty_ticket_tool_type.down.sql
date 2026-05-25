-- Remove zenduty from ticket tool types
DELETE FROM "public"."ticket_tool_types" WHERE "value" = 'zenduty';
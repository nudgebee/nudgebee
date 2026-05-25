DELETE FROM "public"."event_source" WHERE EXISTS (SELECT 1 FROM "public"."event_source" WHERE "value" = 'workflow') AND "value" = 'workflow';

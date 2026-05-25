INSERT INTO "public"."event_source"("value") VALUES ('automation') ON CONFLICT DO NOTHING;
UPDATE "public"."events" SET "source" = 'automation' WHERE "source" = 'workflow';
DELETE FROM "public"."event_source" WHERE EXISTS (SELECT 1 FROM "public"."event_source" WHERE "value" = 'workflow') AND "value" = 'workflow';

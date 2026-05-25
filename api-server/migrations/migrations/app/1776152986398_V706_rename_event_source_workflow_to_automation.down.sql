INSERT INTO "public"."event_source"("value") VALUES ('workflow') ON CONFLICT DO NOTHING;
UPDATE "public"."events" SET "source" = 'workflow' WHERE "source" = 'automation';
DELETE FROM "public"."event_source" WHERE "value" = 'automation';

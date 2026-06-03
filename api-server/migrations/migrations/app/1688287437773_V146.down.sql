
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation_status_type" add column "description" text
--  null;

DELETE FROM "public"."recommendation_status_type" WHERE "value" = 'Archive';

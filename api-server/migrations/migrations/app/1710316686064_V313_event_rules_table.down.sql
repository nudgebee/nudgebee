
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."event_rules" add column "group" text
--  null;

ALTER TABLE "public"."event_rules" ALTER COLUMN "id" drop default;


alter table "public"."events" drop constraint "events_status_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."events" add column "status" text
--  null;

DELETE FROM "public"."event_status" WHERE "value" = 'CLOSED';


alter table "public"."auto_pilot" drop constraint "auto_pilot_update_by_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot" add column "update_by" uuid
--  null;

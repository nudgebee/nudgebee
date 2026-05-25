
alter table "public"."recommendation" drop constraint "recommendation_updated_by_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation" add column "updated_by" uuid
--  null;


alter table "public"."business_unit" drop constraint "business_unit_parent_business_unit_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."business_unit" add column "parent_business_unit" uuid
--  null;

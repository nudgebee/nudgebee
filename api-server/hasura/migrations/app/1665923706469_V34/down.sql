
ALTER TABLE "public"."compliance_standard" ALTER COLUMN "name" TYPE character varying;

alter table "public"."compliance_standard" drop constraint "compliance_standard_owner_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_standard" add column "owner" uuid
--  null;

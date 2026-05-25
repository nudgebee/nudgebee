
ALTER TABLE "public"."funding_sources" ALTER COLUMN "name" TYPE text;

alter table "public"."funding_sources" alter column "updated_by" drop not null;

alter table "public"."funding_sources" alter column "created_by" drop not null;

alter table "public"."funding_sources" drop constraint "funding_sources_tenant_name_key";

alter table "public"."funding_sources" drop constraint "funding_sources_updated_by_fkey";

alter table "public"."funding_sources" drop constraint "funding_sources_created_by_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."funding_sources" add column "updated_by" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."funding_sources" add column "created_by" uuid
--  null;

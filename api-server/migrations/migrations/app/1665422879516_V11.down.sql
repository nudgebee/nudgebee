
ALTER TABLE "public"."user_groups" ALTER COLUMN "id" drop default;

alter table "public"."spends" rename column "date" to "start_date";

alter table "public"."spends" alter column "end_date" drop not null;
alter table "public"."spends" add column "end_date" timestamp;

DROP TABLE "public"."spends";

alter table "public"."user_groups" drop constraint "user_groups_business_unit_fkey";

alter table "public"."user_groups" drop constraint "user_groups_tenant_fkey";

alter table "public"."user_groups" alter column "tenant" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."user_groups" add column "tenant" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."user_groups" add column "business_unit" uuid
--  not null;

alter table "public"."funding_sources" drop constraint "funding_sources_tenant_fkey";

alter table "public"."funding_sources" drop constraint "funding_sources_business_unit_fkey";

ALTER TABLE "public"."funding_sources" ALTER COLUMN "updated_at" TYPE timestamp with time zone;

ALTER TABLE "public"."funding_sources" ALTER COLUMN "created_at" TYPE timestamp with time zone;

ALTER TABLE "public"."funding_sources" ALTER COLUMN "end_date" TYPE timestamp with time zone;

ALTER TABLE "public"."funding_sources" ALTER COLUMN "start_date" TYPE timestamp with time zone;

alter table "public"."funding_sources" alter column "tenant" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."funding_sources" add column "tenant" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."funding_sources" add column "business_unit" uuid
--  not null;

DROP TABLE "public"."funding_sources";

DROP TABLE "public"."user_groups";

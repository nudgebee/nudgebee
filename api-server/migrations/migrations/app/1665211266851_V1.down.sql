
ALTER TABLE "public"."users" ALTER COLUMN "username" TYPE character varying;

ALTER TABLE "public"."tenant" ALTER COLUMN "name" TYPE character varying;

ALTER TABLE "public"."projects" ALTER COLUMN "name" TYPE character varying;

ALTER TABLE "public"."business_unit" ALTER COLUMN "name" TYPE character varying;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE EXTENSION IF NOT EXISTS citext;

alter table "public"."projects" drop constraint "projects_business_unit_name_key";

alter table "public"."business_unit" drop constraint "business_unit_name_tenant_key";

alter table "public"."businessunit_users" alter column "tenant_user" drop not null;

alter table "public"."tenant" drop constraint "tenant_name_key";

alter table "public"."businessunit_users" drop constraint "businessunit_users_tenant_user_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."businessunit_users" add column "tenant_user" uuid
--  null;

DROP TABLE "public"."tenant_users";

ALTER TABLE "public"."business_unit" ALTER COLUMN "id" drop default;

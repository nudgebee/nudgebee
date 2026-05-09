
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_check_findings" add column "hash_code" text
--  null;

alter table "public"."compliance_check" drop constraint "compliance_check_account_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_check" add column "account" uuid
--  null;

alter table "public"."compliance_check" drop constraint "compliance_check_project_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_check" add column "project" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_standard" add column "project" uuid
--  null;

ALTER TABLE "public"."cloud_accounts" ALTER COLUMN "updated_at" TYPE timestamp with time zone;

ALTER TABLE "public"."cloud_accounts" ALTER COLUMN "created_at" TYPE timestamp with time zone;

alter table "public"."cloud_accounts" drop constraint "cloud_accounts_cloud_provider_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "region" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "assume_role" text
--  not null;

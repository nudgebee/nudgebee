
alter table "public"."spends" drop constraint "spends_cloud_resource_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."spends" add column "cloud_resource_id" uuid
--  null;

alter table "public"."cloud_resourses" drop constraint "cloud_resourses_account_arn_key";

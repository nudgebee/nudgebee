
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "synced_at" timestamp
--  null;

alter table "public"."cloud_resourses" drop constraint "cloud_resourses_status_fkey";

DELETE FROM "public"."cloud_resource_status_type" WHERE "value" = 'Deleted';

DELETE FROM "public"."cloud_resource_status_type" WHERE "value" = 'Active';

DROP TABLE "public"."cloud_resource_status_type";

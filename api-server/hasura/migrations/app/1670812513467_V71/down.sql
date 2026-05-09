
alter table "public"."cloud_resource_job_schedule" drop constraint "cloud_resource_job_schedule_status_fkey";

DELETE FROM "public"."cloud_resource_job_schedule_status_type" WHERE "value" = 'Disabled';

DELETE FROM "public"."cloud_resource_job_schedule_status_type" WHERE "value" = 'Error';

DELETE FROM "public"."cloud_resource_job_schedule_status_type" WHERE "value" = 'Active';

DELETE FROM "public"."cloud_resource_job_schedule_status_type" WHERE "value" = 'Pending';

DROP TABLE "public"."cloud_resource_job_schedule_status_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule" add column "status_message" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule" add column "status" text
--  not null default 'Pending';

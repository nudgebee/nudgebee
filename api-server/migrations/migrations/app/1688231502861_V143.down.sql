
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule_events" add column "status_message" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule_events" add column "execution_time" integer
--  null default '0';

ALTER TABLE "public"."cloud_resource_job_schedule_events" ALTER COLUMN "updated_records" drop default;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule_events" add column "failed_records" jsonb
--  null default '[]';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule_events" add column "inserted_records" jsonb
--  null default '[]';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule_events" add column "deleted_records" jsonb
--  null default '[]';

alter table "public"."cloud_resource_job_schedule_events" rename column "updated_records" to "data";

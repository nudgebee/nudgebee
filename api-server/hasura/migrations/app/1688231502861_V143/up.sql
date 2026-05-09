
alter table "public"."cloud_resource_job_schedule_events" rename column "data" to "updated_records";

alter table "public"."cloud_resource_job_schedule_events" add column "deleted_records" jsonb
 null default '[]';

alter table "public"."cloud_resource_job_schedule_events" add column "inserted_records" jsonb
 null default '[]';

alter table "public"."cloud_resource_job_schedule_events" add column "failed_records" jsonb
 null default '[]';

alter table "public"."cloud_resource_job_schedule_events" alter column "updated_records" set default '[]';

alter table "public"."cloud_resource_job_schedule_events" add column "execution_time" integer
 null default '0';

alter table "public"."cloud_resource_job_schedule_events" add column "status_message" text
 null;

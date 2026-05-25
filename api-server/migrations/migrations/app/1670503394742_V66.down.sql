
DROP TABLE "public"."cloud_resource_job_schedule_events";

alter table "public"."cloud_resource_job_schedule" drop constraint "cloud_resource_job_schedule_schedule_unit_fkey";

DELETE FROM "public"."schedule_unit_type" WHERE "value" = 'Cron';

DROP TABLE "public"."schedule_unit_type";

alter table "public"."cloud_resource_job_schedule" drop constraint "cloud_resource_job_schedule_action_fkey";

DELETE FROM "public"."cloud_resource_job_action_type" WHERE "value" = 'StateUpdate';

DROP TABLE "public"."cloud_resource_job_action_type";

DROP TABLE "public"."cloud_resource_job_schedule";

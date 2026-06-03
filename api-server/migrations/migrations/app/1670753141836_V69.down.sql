
alter table "public"."cloud_resource_job_schedule_events" drop constraint "cloud_resource_job_schedule_events_status_fkey";

alter table "public"."cloud_resource_job_schedule_event_status_type" rename to "cloud_resource_job_schedule_event_status";

DELETE FROM "public"."cloud_resource_job_schedule_event_status" WHERE "value" = 'Inprogress';

DELETE FROM "public"."cloud_resource_job_schedule_event_status" WHERE "value" = 'Failed';

DELETE FROM "public"."cloud_resource_job_schedule_event_status" WHERE "value" = 'Success';

DROP TABLE "public"."cloud_resource_job_schedule_event_status";

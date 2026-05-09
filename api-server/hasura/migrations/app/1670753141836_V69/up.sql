
CREATE TABLE "public"."cloud_resource_job_schedule_event_status" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."cloud_resource_job_schedule_event_status"("value") VALUES (E'Success');

INSERT INTO "public"."cloud_resource_job_schedule_event_status"("value") VALUES (E'Failed');

INSERT INTO "public"."cloud_resource_job_schedule_event_status"("value") VALUES (E'Inprogress');

alter table "public"."cloud_resource_job_schedule_event_status" rename to "cloud_resource_job_schedule_event_status_type";

alter table "public"."cloud_resource_job_schedule_events"
  add constraint "cloud_resource_job_schedule_events_status_fkey"
  foreign key ("status")
  references "public"."cloud_resource_job_schedule_event_status_type"
  ("value") on update restrict on delete restrict;


alter table "public"."cloud_resource_job_schedule" add column "status" text
 not null default 'Pending';

alter table "public"."cloud_resource_job_schedule" add column "status_message" text
 null;

CREATE TABLE "public"."cloud_resource_job_schedule_status_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."cloud_resource_job_schedule_status_type"("value") VALUES (E'Pending');

INSERT INTO "public"."cloud_resource_job_schedule_status_type"("value") VALUES (E'Active');

INSERT INTO "public"."cloud_resource_job_schedule_status_type"("value") VALUES (E'Error');

INSERT INTO "public"."cloud_resource_job_schedule_status_type"("value") VALUES (E'Disabled');

alter table "public"."cloud_resource_job_schedule"
  add constraint "cloud_resource_job_schedule_status_fkey"
  foreign key ("status")
  references "public"."cloud_resource_job_schedule_status_type"
  ("value") on update restrict on delete restrict;

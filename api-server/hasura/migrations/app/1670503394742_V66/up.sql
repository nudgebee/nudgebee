
CREATE TABLE "public"."cloud_resource_job_schedule" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "resource_id" uuid NOT NULL, "started_at" timestamp NOT NULL DEFAULT now(), "ended_at" timestamp, "schedule" text NOT NULL, "schedule_unit" text NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL, "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "action" text NOT NULL, "action_config" jsonb, PRIMARY KEY ("id") , FOREIGN KEY ("resource_id") REFERENCES "public"."cloud_resourses"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("resource_id", "action"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."cloud_resource_job_action_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."cloud_resource_job_action_type"("value") VALUES (E'StateUpdate');

alter table "public"."cloud_resource_job_schedule"
  add constraint "cloud_resource_job_schedule_action_fkey"
  foreign key ("action")
  references "public"."cloud_resource_job_action_type"
  ("value") on update restrict on delete restrict;

CREATE TABLE "public"."schedule_unit_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."schedule_unit_type"("value") VALUES (E'Cron');

alter table "public"."cloud_resource_job_schedule"
  add constraint "cloud_resource_job_schedule_schedule_unit_fkey"
  foreign key ("schedule_unit")
  references "public"."schedule_unit_type"
  ("value") on update restrict on delete restrict;

CREATE TABLE "public"."cloud_resource_job_schedule_events" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "cloud_resource_job_schedule_id" uuid NOT NULL, "status" text NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "data" jsonb, PRIMARY KEY ("id") , FOREIGN KEY ("cloud_resource_job_schedule_id") REFERENCES "public"."cloud_resource_job_schedule"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;


CREATE TABLE "public"."auto_pilot_status" ("value" varchar NOT NULL, "description" text NOT NULL, PRIMARY KEY ("value") );COMMENT ON TABLE "public"."auto_pilot_status" IS E'status enum for auto pilot status';

alter table "public"."schedules" rename to "auto_pilot";

ALTER TABLE "public"."auto_pilot_status" ALTER COLUMN "value" TYPE text;

alter table "public"."auto_pilot" add column "status" text
 not null default 'Active';

alter table "public"."auto_pilot"
  add constraint "auto_pilot_status_fkey"
  foreign key ("status")
  references "public"."auto_pilot_status"
  ("value") on update restrict on delete restrict;

alter table "public"."scheduled_task" rename to "auto_pilot_task";

alter table "public"."auto_pilot" add column "source" text
 null;

ALTER TABLE "public"."auto_pilot" ALTER COLUMN "creation_date" TYPE timestamp;

ALTER TABLE "public"."auto_pilot" ALTER COLUMN "update_date" TYPE timestamp;

alter table "public"."auto_pilot" add column "last_schedule_time" timestamp
 null;

alter table "public"."auto_pilot" add column "last_executed_time" timestamp
 null;

alter table "public"."auto_pilot_task" rename column "schedule_id" to "auto_pilot_id";

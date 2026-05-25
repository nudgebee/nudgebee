
alter table "public"."auto_pilot_task" rename column "auto_pilot_id" to "schedule_id";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot" add column "last_executed_time" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot" add column "last_schedule_time" timestamp
--  null;

ALTER TABLE "public"."auto_pilot" ALTER COLUMN "update_date" TYPE date;

ALTER TABLE "public"."auto_pilot" ALTER COLUMN "creation_date" TYPE date;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot" add column "source" text
--  null;

alter table "public"."auto_pilot_task" rename to "scheduled_task";

alter table "public"."auto_pilot" drop constraint "auto_pilot_status_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot" add column "status" text
--  not null default 'Active';

ALTER TABLE "public"."auto_pilot_status" ALTER COLUMN "value" TYPE character varying;

alter table "public"."auto_pilot" rename to "schedules";

DROP TABLE "public"."auto_pilot_status";

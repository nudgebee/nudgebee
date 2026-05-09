
alter table "public"."auto_pilot_task" drop constraint "auto_pilot_task_status_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot_task" add column "status" text
--  not null default 'Scheduled';

DELETE FROM "public"."auto_pilot_task_status" WHERE "value" = 'Scheduled';

DROP TABLE "public"."auto_pilot_task_status";

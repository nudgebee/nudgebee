

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot_task" add column "meta" jsonb
--  not null default jsonb_build_object();


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot_task" add column "meta" jsonb
--  not null default jsonb_build_object();

DELETE FROM "public"."auto_pilot_execution_status" WHERE "value" = 'InProgress';

DELETE FROM "public"."auto_pilot_execution_status" WHERE "value" = 'Idle';


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

alter table "public"."auto_pilot" drop constraint "auto_pilot_execution_status_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot" add column "status" text
--  not null default 'Active';

DROP TABLE "public"."auto_pilot_execution_status";


ALTER TABLE "public"."auto_pilot_task" ALTER COLUMN "scheduled_time" TYPE date;

DELETE FROM "public"."auto_pilot_task_status" WHERE "value" = 'Failed';

DELETE FROM "public"."auto_pilot_task_status" WHERE "value" = 'Complete';

DELETE FROM "public"."auto_pilot_task_status" WHERE "value" = 'Executed';

DELETE FROM "public"."auto_pilot_task_status" WHERE "value" = 'Dryrun';

DELETE FROM "public"."auto_pilot_status" WHERE "value" = 'Dryrun';

DELETE FROM "public"."auto_pilot_status" WHERE "value" = 'Disabled';

alter table "public"."auto_pilot_task" alter column "task_id" set not null;

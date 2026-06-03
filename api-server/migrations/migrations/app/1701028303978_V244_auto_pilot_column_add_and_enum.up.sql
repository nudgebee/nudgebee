
alter table "public"."auto_pilot_task" alter column "task_id" drop not null;

INSERT INTO "public"."auto_pilot_status"("value", "description") VALUES (E'Disabled', E'the auto pilot is disabled');

INSERT INTO "public"."auto_pilot_status"("value", "description") VALUES (E'Dryrun', E'the auto pilot is on dry run no task is created for this auto pilot');

INSERT INTO "public"."auto_pilot_task_status"("description", "value") VALUES (E'this task is only for dry run no execution will take place', E'Dryrun');

INSERT INTO "public"."auto_pilot_task_status"("description", "value") VALUES (E'the task is executed.', E'Executed');

INSERT INTO "public"."auto_pilot_task_status"("description", "value") VALUES (E'the task is complete after getting acknowledgement.', E'Complete');

INSERT INTO "public"."auto_pilot_task_status"("description", "value") VALUES (E'the task has got failed acknowledgement.', E'Failed');

ALTER TABLE "public"."auto_pilot_task" ALTER COLUMN "scheduled_time" TYPE timestamp;

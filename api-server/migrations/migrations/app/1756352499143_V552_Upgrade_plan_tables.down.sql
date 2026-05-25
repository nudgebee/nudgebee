
alter table "public"."upgrade_plan_tasks" drop constraint "upgrade_plan_tasks_status_fkey";

DROP TABLE "public"."upgrade_plan_tasks";

DROP TABLE "public"."upgrade_plan_steps";

DELETE FROM "public"."upgrade_plan_status_type" WHERE "value" = 'Failed';

DELETE FROM "public"."upgrade_plan_status_type" WHERE "value" = 'Skipped';

DELETE FROM "public"."upgrade_plan_status_type" WHERE "value" = 'Completed';

DELETE FROM "public"."upgrade_plan_status_type" WHERE "value" = 'Pending';

DROP TABLE "public"."upgrade_plan_status_type";

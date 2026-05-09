
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_tasks" add column "is_required" boolean
--  not null default 'false';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_tasks" add column "resource_type" text
--  null;

DELETE FROM "public"."upgrade_plan_status_type" WHERE "value" = 'Incomplete';

ALTER TABLE "public"."upgrade_plan_tasks" DROP COLUMN IF EXISTS "is_required";

ALTER TABLE "public"."upgrade_plan_tasks" DROP COLUMN IF EXISTS "resource_type";

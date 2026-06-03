
alter table "public"."upgrade_plan_tasks" drop constraint "upgrade_plan_tasks_owner_fkey";

alter table "public"."upgrade_plan_tasks" drop constraint "upgrade_plan_tasks_updated_by_fkey";

alter table "public"."upgrade_plan_tasks" drop constraint "upgrade_plan_tasks_created_by_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_tasks" add column "owner" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_tasks" add column "updated_by" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_tasks" add column "created_by" uuid
--  null;

alter table "public"."upgrade_plan" drop constraint "upgrade_plan_owner_fkey";

alter table "public"."upgrade_plan" drop constraint "upgrade_plan_updated_by_fkey";

alter table "public"."upgrade_plan" drop constraint "upgrade_plan_created_by_fkey";

alter table "public"."upgrade_plan_steps" drop constraint "upgrade_plan_steps_owner_fkey";

alter table "public"."upgrade_plan_steps" drop constraint "upgrade_plan_steps_updated_by_fkey";

alter table "public"."upgrade_plan_steps" drop constraint "upgrade_plan_steps_created_by_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_steps" add column "owner" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_steps" add column "updated_by" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_steps" add column "created_by" uuid
--  null;

alter table "public"."upgrade_plan_steps" drop constraint "upgrade_plan_steps_plan_id_fkey";

DROP TABLE "public"."upgrade_plan";

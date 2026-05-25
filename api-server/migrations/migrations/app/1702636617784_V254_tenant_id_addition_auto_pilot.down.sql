
alter table "public"."auto_pilot_task" drop constraint "auto_pilot_task_tenant_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot_task" add column "tenant_id" uuid
--  not null;

alter table "public"."auto_pilot" drop constraint "auto_pilot_tenant_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot" add column "tenant_id" uuid
--  not null;

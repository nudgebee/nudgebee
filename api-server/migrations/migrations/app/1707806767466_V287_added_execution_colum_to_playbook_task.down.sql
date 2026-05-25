
alter table "public"."auto_playbook_task" drop constraint "auto_playbook_task_execution_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_playbook_task" add column "execution_id" uuid
--  null;

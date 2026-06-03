
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_playbook_task" add column "task_type" text
--  not null;

alter table "public"."auto_playbook" rename column "trigger" to "source";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_playbook_task" add column "resource_id" uuid
--  not null;

alter table "public"."auto_playbook_task" drop constraint "auto_playbook_task_auto_playbook_id_fkey2";

alter table "public"."auto_playbook_task" drop constraint "auto_playbook_task_account_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_playbook_task" add column "account_id" uuid
--  not null;

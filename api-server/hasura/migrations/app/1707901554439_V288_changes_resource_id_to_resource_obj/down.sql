
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_playbook_task" add column "resource" jsonb
--  null default jsonb_build_object();

alter table "public"."auto_playbook_task" alter column "resource_id" drop not null;
alter table "public"."auto_playbook_task" add column "resource_id" uuid;

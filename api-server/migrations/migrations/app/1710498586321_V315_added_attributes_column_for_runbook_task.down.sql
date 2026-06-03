
alter table "public"."auto_playbook_task" rename column "attributes" to "attribute";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_playbook_task" add column "attribute" jsonb
--  not null default jsonb_build_object();

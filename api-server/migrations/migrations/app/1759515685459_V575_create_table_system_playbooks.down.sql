
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."runbook_action" add column "is_system_action" boolean
--  not null default 'false';


alter table "public"."system_playbook" alter column "enabled" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."system_playbook" add column "enabled" boolean
--  null default 'true';

DROP TABLE "public"."system_playbook";

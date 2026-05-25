
alter table "public"."agent_playbook" drop constraint "agent_playbook_processor_fkey";

alter table "public"."agent_playbook_processor" drop constraint "agent_playbook_processor_value_key";

alter table "public"."agent_playbook" alter column "processor" set default 'nb_agent'::text;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent_playbook" add column "processor" text
--  null default 'nb_agent';

alter table "public"."agent_playbook_processor" rename to "agent_playbook_action_processor";

DELETE FROM "public"."agent_playbook_action_processor" WHERE "name" = 'nb_agent';

DELETE FROM "public"."agent_playbook_action_processor" WHERE "name" = 'nb_server';

DROP TABLE "public"."agent_playbook_action_processor";

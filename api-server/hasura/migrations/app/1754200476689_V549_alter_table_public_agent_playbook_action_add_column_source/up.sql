
alter table "public"."agent_playbook_action" add column "source" text
 null default 'prometheus';

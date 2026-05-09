alter table "public"."auto_playbook_task" add column "action_type" text
 not null default 'agent_task';

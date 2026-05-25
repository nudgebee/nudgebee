
alter table "public"."agent_playbook" add column "alert_name" text
 null;

alter table "public"."agent_playbook_action" add column "display_name" text
 null;

alter table "public"."agent_playbook_action" add column "description" text
 null;

alter table "public"."agent_playbook_action" add column "category" text
 null default 'All';

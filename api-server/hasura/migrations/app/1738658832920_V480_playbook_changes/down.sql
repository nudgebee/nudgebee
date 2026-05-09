
alter table "public"."agent_playbook" drop constraint "agent_playbook_tenant_id_cloud_account_id_alert_name_key";


alter table "public"."agent_playbook" alter column "trigger" drop not null;
alter table "public"."agent_playbook" add column "trigger" text;

alter table "public"."agent_playbook" alter column "action" drop not null;
alter table "public"."agent_playbook" add column "action" text;

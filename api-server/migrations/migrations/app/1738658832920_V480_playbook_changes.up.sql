

alter table "public"."agent_playbook" drop column "action" cascade;

alter table "public"."agent_playbook" drop column "trigger" cascade;

alter table "public"."agent_playbook" add constraint "agent_playbook_tenant_id_cloud_account_id_alert_name_key" unique ("tenant_id", "cloud_account_id", "alert_name");

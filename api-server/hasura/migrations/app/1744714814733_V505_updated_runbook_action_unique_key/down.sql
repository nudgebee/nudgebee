alter table "public"."runbook_action" drop constraint "runbook_action_action_name_tenant_id_key";
alter table "public"."runbook_action" add constraint "runbook_action_library_id_action_name_key" unique ("library_id", "action_name");

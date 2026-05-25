alter table "public"."runbook_action" drop constraint "runbook_action_action_name_library_id_key";
alter table "public"."runbook_action" add constraint "runbook_action_action_name_tenant_id_key" unique ("action_name", "tenant_id");

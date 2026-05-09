delete from runbook_action ra where tenant_id is not null;
alter table "public"."runbook_action" add constraint "runbook_action_action_name_library_id_key" unique ("action_name", "library_id");

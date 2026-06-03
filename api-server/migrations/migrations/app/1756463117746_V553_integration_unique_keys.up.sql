
alter table "public"."integrations" add constraint "integrations_tenant_id_account_id_type_source_name_key" unique ("tenant_id", "account_id", "type", "source", "name");

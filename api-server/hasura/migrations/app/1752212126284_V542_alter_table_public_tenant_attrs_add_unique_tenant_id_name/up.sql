
alter table "public"."tenant_attrs" add constraint "tenant_attrs_tenant_id_name_key" unique ("tenant_id", "name");

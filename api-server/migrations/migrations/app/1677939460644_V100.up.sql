
alter table "public"."spends" add constraint "spends_tenant_cloud_account_cloud_resource_id_date_key" unique ("tenant", "cloud_account", "cloud_resource_id", "date");

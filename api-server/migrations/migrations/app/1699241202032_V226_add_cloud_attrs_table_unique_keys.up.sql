

alter table "public"."cloud_account_attrs" add constraint "cloud_account_attrs_cloud_account_id_name_key" unique ("cloud_account_id", "name");

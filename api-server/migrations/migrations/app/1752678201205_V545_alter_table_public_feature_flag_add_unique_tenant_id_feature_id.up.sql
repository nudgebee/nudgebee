
alter table "public"."feature_flag" drop constraint "feature_flag_feature_id_tenant_id_feature_module_id_key";
alter table "public"."feature_flag" add constraint "feature_flag_tenant_id_feature_id_key" unique ("tenant_id", "feature_id");

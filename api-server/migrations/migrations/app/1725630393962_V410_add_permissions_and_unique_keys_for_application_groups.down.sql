
alter table "public"."application_group_mapping" drop constraint "application_group_mapping_workload_kind_group_id_namespace_name_workload_name_account_id_key";
alter table "public"."application_group_mapping" add constraint "application_group_mapping_workload_kind_group_id_namespace_name_workload_name_key" unique ("workload_kind", "group_id", "namespace_name", "workload_name");

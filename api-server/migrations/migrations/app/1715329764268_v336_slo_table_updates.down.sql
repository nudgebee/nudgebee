
alter table "public"."slo_config" drop constraint "slo_config_workload_namespace_name_workload_name_tenant_id_cloud_account_id_key";
alter table "public"."slo_config" add constraint "slo_config_workload_namespace_name_workload_name_key" unique ("workload_namespace", "name", "workload_name");

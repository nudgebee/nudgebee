
alter table "public"."slo_report" add column "timestamp" timestamp
 not null;

alter table "public"."slo_report" drop constraint "slo_report_config_id_workload_name_workload_namespace_cloud_key";
alter table "public"."slo_report" add constraint "slo_report_config_id_tenant_id_workload_name_workload_namespace_cloud_account_id_timestamp_key" unique ("config_id", "tenant_id", "workload_name", "workload_namespace", "cloud_account_id", "timestamp");

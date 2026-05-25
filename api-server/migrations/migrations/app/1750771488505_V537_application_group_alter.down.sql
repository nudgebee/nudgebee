
alter table "public"."application_group_mapping" drop constraint "application_group_mapping_workload_name_workload_kind_namesp";

alter table "public"."k8s_workloads" drop constraint "k8s_workloads_cloud_account_id_kind_namespace_name_key";
alter table "public"."k8s_workloads" add constraint "k8s_workloads_name_cloud_account_id_namespace_tenant_id_kind_cloud_resource_id_key" unique ("name", "cloud_account_id", "namespace", "tenant_id", "kind", "cloud_resource_id");

alter table "public"."k8s_workloads" drop constraint "k8s_workloads_cloud_account_id_cloud_resource_id_name_namespace_kind_tenant_id_key";

alter table "public"."application_group" alter column "updated_at" drop not null;

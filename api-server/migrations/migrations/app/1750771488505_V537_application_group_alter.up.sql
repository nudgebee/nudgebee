
alter table "public"."application_group" alter column "updated_at" set not null;

alter table "public"."k8s_workloads" add constraint "k8s_workloads_cloud_account_id_cloud_resource_id_name_namespace_kind_tenant_id_key" unique ("cloud_account_id", "cloud_resource_id", "name", "namespace", "kind", "tenant_id");

alter table "public"."k8s_workloads" drop constraint "k8s_workloads_cloud_account_id_cloud_resource_id_name_namespace";
alter table "public"."k8s_workloads" add constraint "k8s_workloads_cloud_account_id_kind_namespace_name_key" unique ("cloud_account_id", "kind", "namespace", "name");

alter table "public"."application_group_mapping"
  add constraint "application_group_mapping_workload_name_workload_kind_namesp"
  foreign key ("workload_name", "workload_kind", "namespace_name", "account_id")
  references "public"."k8s_workloads"
  ("name", "kind", "namespace", "cloud_account_id") on update restrict on delete restrict;

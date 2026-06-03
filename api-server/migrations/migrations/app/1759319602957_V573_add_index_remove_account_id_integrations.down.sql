
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE INDEX IF NOT EXISTS idx_workloads_active_tenant_cloud_account_kind_name_namespace ON k8s_workloads (cloud_account_id, tenant_id, kind, namespace, name, is_active) WHERE is_active = TRUE;
-- CREATE INDEX IF NOT EXISTS idx_nodes_active_tenant ON k8s_nodes (cloud_account_id, tenant_id) WHERE is_active = TRUE;
-- CREATE INDEX IF NOT EXISTS idx_pods_active_tenant_status ON k8s_pods (cloud_account_id, tenant_id, status) WHERE is_active = TRUE;
--
-- ALTER TABLE integrations DROP COLUMN IF EXISTS account_id;

CREATE  INDEX "k8s_workloads_tenant_cloud_active_kind_creation" on
  "public"."k8s_workloads" using btree ("cloud_account_id", "creation_time", "is_active", "kind", "tenant_id");

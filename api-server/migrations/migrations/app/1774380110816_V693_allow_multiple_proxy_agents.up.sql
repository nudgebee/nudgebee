-- Allow multiple proxy agents per account (one per vm_agent integration).
-- Keep uniqueness for non-proxy agent types (AWS, Azure, GCP, k8s, etc.).

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_tenant_account_type;

CREATE UNIQUE INDEX agent_tenant_account_type_non_proxy
  ON agent (tenant, cloud_account_id, type)
  WHERE type != 'proxy';

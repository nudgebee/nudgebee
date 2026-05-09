DROP INDEX IF EXISTS agent_tenant_account_type_non_proxy;

ALTER TABLE agent ADD CONSTRAINT agent_tenant_account_type UNIQUE (tenant, cloud_account_id, type);

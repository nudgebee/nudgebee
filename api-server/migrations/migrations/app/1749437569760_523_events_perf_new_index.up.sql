CREATE INDEX if not exists events_tenant_cloud_created_idx ON events (tenant, cloud_account_id, created_at DESC);

CREATE INDEX idx_events_tenant_account_source
ON events (tenant, cloud_account_id, source)
WHERE source IS NOT NULL;

-- Index for tenant-wide event queries filtered by created_at (without cloud_account_id)
-- Fixes timeout on event_groupings_v2 queries like list_k8_issues_count
CREATE INDEX IF NOT EXISTS events_tenant_created_idx ON events (tenant, created_at DESC);
